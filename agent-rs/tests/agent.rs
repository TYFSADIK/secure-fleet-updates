use ed25519_dalek::{Signer, SigningKey};
use fleet_ota_agent::*;
use sha2::{Digest, Sha256};

fn signed(sk: &SigningKey, ver: &str, img: &[u8]) -> Manifest {
    let mut m = Manifest { device_class: "rover-pi5".into(), version: ver.into(),
        sha256: hex::encode(Sha256::digest(img)), size: img.len() as i64, signature: String::new() };
    m.signature = hex::encode(sk.sign(&m.payload()).to_bytes());
    m
}

fn agent(dir: &std::path::Path) -> (Agent, SigningKey) {
    let sk = SigningKey::generate(&mut rand::rngs::OsRng);
    (Agent { dir: dir.into(), key: Some(sk.verifying_key()), device: "rover-pi5".into(), boot_tries: 2 }, sk)
}

#[test]
fn happy_path() {
    let d = tempfile::tempdir().unwrap();
    let (a, sk) = agent(d.path());
    let img = b"rootfs v1.1.0";
    assert_eq!(a.stage(&signed(&sk, "1.1.0", img), img).unwrap(), "B");
    assert_eq!(a.boot().unwrap(), "B");
    assert!(a.confirm().unwrap());
    let s = a.load().unwrap();
    assert_eq!((s.active.as_str(), s.min_version.as_str()), ("B", "1.1.0"));
}

#[test]
fn automatic_rollback() {
    let d = tempfile::tempdir().unwrap();
    let (a, sk) = agent(d.path());
    a.stage(&signed(&sk, "1.1.0", b"bad"), b"bad").unwrap();
    a.boot().unwrap();
    a.boot().unwrap();
    assert_eq!(a.boot().unwrap(), "A");
    assert_eq!(a.load().unwrap().rolled_back, 1);
}

#[test]
fn rejections() {
    let d = tempfile::tempdir().unwrap();
    let (a, sk) = agent(d.path());
    let evil = SigningKey::generate(&mut rand::rngs::OsRng);
    assert_eq!(a.stage(&signed(&evil, "9.0.0", b"x"), b"x"), Err(OtaError::Signature));
    assert_eq!(a.stage(&signed(&sk, "2.0.0", b"x"), b"y"), Err(OtaError::Digest));
    a.stage(&signed(&sk, "2.0.0", b"x"), b"x").unwrap();
    assert_eq!(a.stage(&signed(&sk, "3.0.0", b"x"), b"x"), Err(OtaError::Busy));
    a.boot().unwrap();
    a.confirm().unwrap();
    assert_eq!(a.stage(&signed(&sk, "1.9.9", b"x"), b"x"), Err(OtaError::Rollback));
}

#[test]
fn go_compatible_payload() {
    // Go: json.Marshal(Manifest{...}) with Signature "" (omitempty) -> exactly this byte string.
    let m = Manifest { device_class: "rover-pi5".into(), version: "1.2.0".into(), sha256: "ab".into(), size: 3, signature: "zz".into() };
    assert_eq!(String::from_utf8(m.payload()).unwrap(),
               r#"{"device_class":"rover-pi5","version":"1.2.0","sha256":"ab","size":3}"#);
}
