//! A/B over-the-air update agent for the robot fleet.
//!
//! Same rules and file formats as the Go agent in `../ota`, so the Go release tooling
//! (manifest signing, campaigns) works unchanged:
//! * manifest signature (Ed25519) over the manifest JSON *without* the `signature` field,
//!   byte-for-byte identical to Go's `json.Marshal` of the struct;
//! * monotonic anti-rollback floor;
//! * N trial boots, then automatic fallback unless `confirm` ran after a healthy boot;
//! * state written atomically (tmp + fsync + rename + fsync dir).
//!
//! Why Rust on the device: it runs as root and decides what boots, so memory safety without
//! a garbage collector, a ~1 MB static binary, and no runtime are worth it.

use ed25519_dalek::{Signature, Verifier, VerifyingKey};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    cmp::Ordering,
    collections::BTreeMap,
    fs::{self, File},
    io::Write,
    path::{Path, PathBuf},
};

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Manifest {
    pub device_class: String,
    pub version: String,
    pub sha256: String,
    pub size: i64,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub signature: String,
}

impl Manifest {
    /// Bytes covered by the signature: the manifest with `signature` empty (omitted).
    pub fn payload(&self) -> Vec<u8> {
        let mut m = self.clone();
        m.signature.clear();
        serde_json::to_vec(&m).expect("manifest serialises")
    }

    pub fn verify_sig(&self, key: &VerifyingKey) -> bool {
        let Ok(raw) = hex::decode(&self.signature) else { return false };
        let Ok(arr) = <[u8; 64]>::try_from(raw.as_slice()) else { return false };
        key.verify(&self.payload(), &Signature::from_bytes(&arr)).is_ok()
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct State {
    pub active: String,
    pub pending: String,
    pub tries_left: i32,
    pub versions: BTreeMap<String, String>,
    pub min_version: String,
    pub last_booted: String,
    pub rolled_back: i32,
}

impl Default for State {
    fn default() -> Self {
        State {
            active: "A".into(),
            pending: String::new(),
            tries_left: 0,
            versions: BTreeMap::from([("A".to_string(), "0.0.0".to_string())]),
            min_version: "0.0.0".into(),
            last_booted: String::new(),
            rolled_back: 0,
        }
    }
}

#[derive(Debug, thiserror::Error, PartialEq)]
pub enum OtaError {
    #[error("manifest signature invalid")]
    Signature,
    #[error("image built for another device class")]
    Device,
    #[error("version is not newer than the anti-rollback floor")]
    Rollback,
    #[error("image digest mismatch")]
    Digest,
    #[error("an update is already pending")]
    Busy,
    #[error("io: {0}")]
    Io(String),
}

impl From<std::io::Error> for OtaError {
    fn from(e: std::io::Error) -> Self {
        OtaError::Io(e.to_string())
    }
}

pub fn compare_semver(a: &str, b: &str) -> Ordering {
    let parse = |s: &str| -> [u64; 3] {
        let mut out = [0; 3];
        for (i, p) in s.splitn(3, '.').enumerate() {
            out[i] = p.parse().unwrap_or(0);
        }
        out
    };
    parse(a).cmp(&parse(b))
}

fn other(slot: &str) -> &'static str {
    if slot == "A" { "B" } else { "A" }
}

pub struct Agent {
    pub dir: PathBuf,
    /// Pinned release key. Only `stage` needs it; boot/confirm must work even if it is missing.
    pub key: Option<VerifyingKey>,
    pub device: String,
    pub boot_tries: i32,
}

impl Agent {
    fn state_path(&self) -> PathBuf {
        self.dir.join("state.json")
    }

    pub fn load(&self) -> Result<State, OtaError> {
        match fs::read(self.state_path()) {
            Ok(b) => serde_json::from_slice(&b).map_err(|e| OtaError::Io(e.to_string())),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(State::default()),
            Err(e) => Err(e.into()),
        }
    }

    fn save(&self, s: &State) -> Result<(), OtaError> {
        atomic_write(&self.state_path(), &serde_json::to_vec_pretty(s).unwrap())
    }

    pub fn stage(&self, m: &Manifest, image: &[u8]) -> Result<String, OtaError> {
        let mut s = self.load()?;
        if !s.pending.is_empty() {
            return Err(OtaError::Busy);
        }
        if !self.key.as_ref().is_some_and(|k| m.verify_sig(k)) {
            return Err(OtaError::Signature);
        }
        if m.device_class != self.device {
            return Err(OtaError::Device);
        }
        let active_ver = s.versions.get(&s.active).cloned().unwrap_or_else(|| "0.0.0".into());
        if compare_semver(&m.version, &s.min_version) != Ordering::Greater
            || compare_semver(&m.version, &active_ver) != Ordering::Greater
        {
            return Err(OtaError::Rollback);
        }
        if hex::encode(Sha256::digest(image)) != m.sha256 || image.len() as i64 != m.size {
            return Err(OtaError::Digest);
        }
        let slot = other(&s.active).to_string();
        // On hardware this is the inactive partition (e.g. /dev/mmcblk0p3); write + fsync before arming.
        atomic_write(&self.dir.join(format!("slot-{slot}.img")), image)?;
        s.pending = slot.clone();
        s.tries_left = self.boot_tries;
        s.versions.insert(slot.clone(), m.version.clone());
        self.save(&s)?;
        Ok(slot)
    }

    /// Bootloader hook: which slot to boot now.
    pub fn boot(&self) -> Result<String, OtaError> {
        let mut s = self.load()?;
        let mut slot = s.active.clone();
        if !s.pending.is_empty() {
            if s.tries_left > 0 {
                s.tries_left -= 1;
                slot = s.pending.clone();
            } else {
                s.pending.clear();
                s.rolled_back += 1;
            }
        }
        s.last_booted = slot.clone();
        self.save(&s)?;
        Ok(slot)
    }

    /// Health check passed: commit the trial slot and raise the anti-rollback floor.
    pub fn confirm(&self) -> Result<bool, OtaError> {
        let mut s = self.load()?;
        if s.pending.is_empty() || s.last_booted != s.pending {
            return Ok(false);
        }
        s.active = std::mem::take(&mut s.pending);
        s.min_version = s.versions[&s.active].clone();
        self.save(&s)?;
        Ok(true)
    }
}

pub fn atomic_write(path: &Path, data: &[u8]) -> Result<(), OtaError> {
    let tmp = path.with_extension("tmp");
    {
        let mut f = File::create(&tmp)?;
        f.write_all(data)?;
        f.sync_all()?;
    }
    fs::rename(&tmp, path)?;
    if let Some(dir) = path.parent() {
        File::open(dir)?.sync_all()?; // make the rename itself durable across power loss
    }
    Ok(())
}
