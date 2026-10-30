package fleet_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/TYFSADIK/secure-fleet-updates/campaign"
	"github.com/TYFSADIK/secure-fleet-updates/measured"
	"github.com/TYFSADIK/secure-fleet-updates/ota"
)

func signed(priv ed25519.PrivateKey, ver string, img []byte) ota.Manifest {
	s := sha256.Sum256(img)
	m := ota.Manifest{Device: "rover-pi5", Version: ver, SHA256: hex.EncodeToString(s[:]), Size: int64(len(img))}
	m.Sign(priv)
	return m
}

func agent(t *testing.T) (*ota.Agent, ed25519.PrivateKey) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	return &ota.Agent{Dir: t.TempDir(), Pub: pub, Device: "rover-pi5", BootTries: 2}, priv
}

func TestHappyPath(t *testing.T) {
	a, priv := agent(t)
	img := []byte("v1.1.0 rootfs")
	if err := a.Stage(signed(priv, "1.1.0", img), img); err != nil {
		t.Fatal(err)
	}
	if slot, _ := a.Boot(); slot != "B" {
		t.Fatalf("trial boot should use B, got %s", slot)
	}
	a.Confirm()
	s, _ := a.Load()
	if s.Active != "B" || s.MinVersion != "1.1.0" {
		t.Fatal(s)
	}
}

func TestAutomaticRollback(t *testing.T) {
	a, priv := agent(t)
	img := []byte("bad build")
	a.Stage(signed(priv, "1.1.0", img), img)
	a.Boot()
	a.Boot() // two trial boots, never confirmed (kernel panic / health check fail)
	if slot, _ := a.Boot(); slot != "A" {
		t.Fatalf("must fall back to A, got %s", slot)
	}
	s, _ := a.Load()
	if s.RolledBack != 1 || s.MinVersion != "0.0.0" {
		t.Fatal(s)
	}
}

func TestRejections(t *testing.T) {
	a, priv := agent(t)
	_, evil, _ := ed25519.GenerateKey(nil)
	img := []byte("x")
	if a.Stage(signed(evil, "9.0.0", img), img) != ota.ErrSignature {
		t.Fatal("foreign key accepted")
	}
	if a.Stage(signed(priv, "2.0.0", img), []byte("y")) != ota.ErrDigest {
		t.Fatal("swapped image accepted")
	}
	a.Stage(signed(priv, "2.0.0", img), img)
	a.Boot()
	a.Confirm()
	if a.Stage(signed(priv, "1.9.9", img), img) != ota.ErrRollback {
		t.Fatal("downgrade accepted")
	}
}

func TestCampaignHaltsOnCanaryFailure(t *testing.T) {
	devs := make([]string, 40)
	for i := range devs {
		devs[i] = string(rune('a' + i%26))
	}
	n := 0
	rep := campaign.Run(devs, campaign.DefaultWaves, 0.2, func(string) campaign.Result {
		n++
		if n <= 2 {
			return campaign.RolledBack
		}
		return campaign.Success
	})
	if !rep[len(rep)-1].Halted || n != 2 {
		t.Fatalf("should halt after canary, touched %d: %+v", n, rep)
	}
}

func TestPCRCompare(t *testing.T) {
	out := "sha256:\n  0 : 0xAAAA\n  7 : 0xBBBB\n"
	got, err := measured.ParsePCRRead(out)
	if err != nil {
		t.Fatal(err)
	}
	if bad := measured.Compare(map[int]string{0: "aaaa", 7: "cccc"}, got); len(bad) != 1 || bad[0] != 7 {
		t.Fatalf("bad=%v", bad)
	}
}
