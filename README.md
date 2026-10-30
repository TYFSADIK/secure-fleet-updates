# Secure Robot-Fleet Updates

Private research repo. Secure boot, TPM 2.0 measured boot, YubiKey-signed over-the-air updates, and automatic A/B rollback for a small robot fleet, tested in a cold chamber.

Build-side tools in **Go**. On-device agent in **Rust** (`agent-rs/`).

Full write-up: [`docs/project-page.md`](docs/project-page.md)

## What's here

| Path | Purpose |
|---|---|
| `ota/agent.go` | A/B agent: signature check against a pinned key, device-class check, **anti-rollback floor**, digest and size check, atomic state file, trial boots, `Confirm()` |
| `campaign/campaign.go` | Wave rollout (canary 5%, then 25%, 50%, all) that halts automatically if a wave's rollback ratio is too high. Unreachable robots don't count as failures |
| `measured/pcr.go` | Parses `tpm2_pcrread` and compares against golden PCRs 0, 2, 4, 7 |
| `cmd/fleet-ota` | Device CLI: `status`, `stage`, `boot`, `confirm`, `pcr` |
| `scripts/` | YubiKey signing notes, systemd health-confirm unit |

## Rust device agent (`agent-rs/`)

The agent that runs **on each robot** is Rust, a **491 KB** static binary. It runs as root and decides what boots, so it gets memory safety with no GC or runtime. It uses exactly the same manifest and state formats as the Go code, so the Go release tooling (`campaign/`, signing) is unchanged. A manifest signed by Go was verified, staged, booted, and confirmed by the Rust agent in the cross-language check.

```bash
(cd agent-rs && cargo test && cargo build --release)
agent-rs/target/release/fleet-ota-agent --dir /var/lib/fleet-ota --pub /etc/fleet-ota/release.pub stage --manifest m.json --image rootfs.img
agent-rs/target/release/fleet-ota-agent boot      # bootloader hook: prints A or B
agent-rs/target/release/fleet-ota-agent confirm   # after the health check passes
# Pi 5: rustup target add aarch64-unknown-linux-musl && cargo zigbuild --release --target aarch64-unknown-linux-musl
```

Go remains the language for the build-side tools (manifests, campaigns, PCR checks), and Rust is the device agent.

## Quickstart

```bash
make test     # happy path, auto-rollback, forged key, swapped image, downgrade, campaign halt, PCR mismatch
make build    # includes linux/arm64 for the Pi
```

## Next steps
- [ ] Wire `fleet-ota boot` into the Pi 5 bootloader (`tryboot` + `autoboot.txt` A/B partitions) or U-Boot `bootcount`
- [ ] Mirror `MinVersion` into a TPM NV monotonic counter so it survives an SD card swap
- [ ] Seal the fleet mTLS key with `tpm2_create --policy pcr:sha256:0,2,4,7` so it unseals only on a golden boot
- [ ] Deliver images through `ddil-proof-devops` delta chunks
- [ ] Cold-chamber test T3: campaign reliability at -20 °C (SD card write errors, brown-outs during write)
