// fleet-ota: device-side agent CLI (run from systemd and the bootloader hook).
//
//	fleet-ota status  -dir /var/lib/fleet-ota
//	fleet-ota stage   -dir ... -pub /etc/fleet-ota/release.pub -manifest m.json -image rootfs.img
//	fleet-ota boot    -dir ...          (prints slot to boot: A|B)
//	fleet-ota confirm -dir ...          (health check passed)
//	fleet-ota pcr     -golden golden.json
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/TYFSADIK/secure-fleet-updates/measured"
	"github.com/TYFSADIK/secure-fleet-updates/ota"
)

func die(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...); os.Exit(1) }

func main() {
	if len(os.Args) < 2 {
		die("usage: fleet-ota status|stage|boot|confirm|pcr")
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	dir := fs.String("dir", "/var/lib/fleet-ota", "")
	pubp := fs.String("pub", "/etc/fleet-ota/release.pub", "")
	device := fs.String("device", "rover-pi5", "")
	mp := fs.String("manifest", "", "")
	ip := fs.String("image", "", "")
	golden := fs.String("golden", "", "")
	fs.Parse(os.Args[2:])
	var pub []byte
	if b, err := os.ReadFile(*pubp); err == nil {
		pub, _ = hex.DecodeString(strings.TrimSpace(string(b)))
	}
	a := &ota.Agent{Dir: *dir, Pub: ed25519.PublicKey(pub), Device: *device, BootTries: 3}
	switch os.Args[1] {
	case "status":
		s, err := a.Load()
		if err != nil {
			die("%v", err)
		}
		fmt.Println(s)
	case "stage":
		var m ota.Manifest
		mb, _ := os.ReadFile(*mp)
		if json.Unmarshal(mb, &m) != nil {
			die("bad manifest")
		}
		img, err := os.ReadFile(*ip)
		if err != nil {
			die("%v", err)
		}
		if err := a.Stage(m, img); err != nil {
			die("REJECTED: %v", err)
		}
		fmt.Println("staged", m.Version)
	case "boot":
		slot, err := a.Boot()
		if err != nil {
			die("%v", err)
		}
		fmt.Println(slot)
	case "confirm":
		if err := a.Confirm(); err != nil {
			die("%v", err)
		}
	case "pcr":
		var g map[int]string
		b, _ := os.ReadFile(*golden)
		if json.Unmarshal(b, &g) != nil {
			die("bad golden file")
		}
		got, err := measured.Read("sha256:0,2,4,7")
		if err != nil {
			die("%v", err)
		}
		if bad := measured.Compare(g, got); len(bad) > 0 {
			die("PCR MISMATCH %v: boot chain changed", bad)
		}
		fmt.Println("PCRs match golden")
	}
}
