//! fleet-ota (Rust device agent)
//!   fleet-ota status  --dir /var/lib/fleet-ota
//!   fleet-ota stage   --dir ... --pub /etc/fleet-ota/release.pub --manifest m.json --image rootfs.img
//!   fleet-ota boot    --dir ...        # prints A|B
//!   fleet-ota confirm --dir ...
use clap::{Parser, Subcommand};
use ed25519_dalek::VerifyingKey;
use fleet_ota_agent::{Agent, Manifest};
use std::{fs, path::PathBuf, process::exit};

#[derive(Parser)]
#[command(version, about = "A/B OTA agent")]
struct Cli {
    #[arg(long, default_value = "/var/lib/fleet-ota")]
    dir: PathBuf,
    #[arg(long = "pub", default_value = "/etc/fleet-ota/release.pub")]
    pubkey: PathBuf,
    #[arg(long, default_value = "rover-pi5")]
    device: String,
    #[arg(long, default_value_t = 3)]
    tries: i32,
    #[command(subcommand)]
    cmd: Cmd,
}

#[derive(Subcommand)]
enum Cmd {
    Status,
    Stage {
        #[arg(long)]
        manifest: PathBuf,
        #[arg(long)]
        image: PathBuf,
    },
    Boot,
    Confirm,
}

fn die(msg: impl std::fmt::Display) -> ! {
    eprintln!("{msg}");
    exit(1)
}

fn load_key(p: &PathBuf) -> VerifyingKey {
    let hexs = fs::read_to_string(p).unwrap_or_else(|e| die(format!("read key: {e}")));
    let raw = hex::decode(hexs.trim()).unwrap_or_else(|e| die(format!("key hex: {e}")));
    let arr: [u8; 32] = raw.try_into().unwrap_or_else(|_| die("key must be 32 bytes"));
    VerifyingKey::from_bytes(&arr).unwrap_or_else(|e| die(format!("key: {e}")))
}

fn main() {
    let cli = Cli::parse();
    let key = matches!(cli.cmd, Cmd::Stage { .. }).then(|| load_key(&cli.pubkey));
    let agent = Agent { dir: cli.dir, key, device: cli.device, boot_tries: cli.tries };
    match cli.cmd {
        Cmd::Status => println!("{}", serde_json::to_string_pretty(&agent.load().unwrap_or_else(|e| die(e))).unwrap()),
        Cmd::Stage { manifest, image } => {
            let m: Manifest = serde_json::from_slice(&fs::read(manifest).unwrap_or_else(|e| die(e)))
                .unwrap_or_else(|e| die(format!("bad manifest: {e}")));
            let img = fs::read(image).unwrap_or_else(|e| die(e));
            match agent.stage(&m, &img) {
                Ok(slot) => println!("staged {} into slot {slot}", m.version),
                Err(e) => die(format!("REJECTED: {e}")),
            }
        }
        Cmd::Boot => println!("{}", agent.boot().unwrap_or_else(|e| die(e))),
        Cmd::Confirm => {
            if agent.confirm().unwrap_or_else(|e| die(e)) {
                println!("confirmed")
            }
        }
    }
}
