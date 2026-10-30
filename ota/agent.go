// Package ota is an A/B over-the-air update agent for a small robot fleet.
//
// Rules:
//   - An image is only staged if its manifest verifies against the pinned
//     release key (held on a YubiKey; the private key never leaves the card).
//   - Versions are monotonic (anti-rollback): an attacker cannot replay an
//     old, signed, vulnerable image.
//   - A new slot gets N boot attempts. If the health check doesn't Confirm()
//     before they run out, the next boot falls back to the last good slot.
package ota

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Manifest struct {
	Device    string `json:"device_class"` // e.g. "rover-pi5"
	Version   string `json:"version"`      // semver MAJOR.MINOR.PATCH
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Signature string `json:"signature,omitempty"`
}

func (m Manifest) payload() []byte {
	c := m
	c.Signature = ""
	b, _ := json.Marshal(c)
	return b
}

func (m *Manifest) Sign(priv ed25519.PrivateKey) {
	m.Signature = hex.EncodeToString(ed25519.Sign(priv, m.payload()))
}

func (m Manifest) VerifySig(pub ed25519.PublicKey) bool {
	s, err := hex.DecodeString(m.Signature)
	return err == nil && ed25519.Verify(pub, m.payload(), s)
}

type State struct {
	Active     string            `json:"active"`  // "A" or "B": slot booted when nothing is pending
	Pending    string            `json:"pending"` // slot under trial, "" if none
	TriesLeft  int               `json:"tries_left"`
	Versions   map[string]string `json:"versions"`
	MinVersion string            `json:"min_version"` // anti-rollback floor (mirror into TPM NV counter on hardware)
	LastBooted string            `json:"last_booted"`
	RolledBack int               `json:"rolled_back"`
}

type Agent struct {
	Dir       string // holds state.json and slot-A.img / slot-B.img (block devices on real hw)
	Pub       ed25519.PublicKey
	Device    string
	BootTries int
}

var (
	ErrSignature = errors.New("manifest signature invalid")
	ErrDevice    = errors.New("image built for another device class")
	ErrRollback  = errors.New("version is not newer than the anti-rollback floor")
	ErrDigest    = errors.New("image digest mismatch")
	ErrBusy      = errors.New("an update is already pending")
)

func (a *Agent) statePath() string { return filepath.Join(a.Dir, "state.json") }

func (a *Agent) Load() (State, error) {
	b, err := os.ReadFile(a.statePath())
	if os.IsNotExist(err) {
		return State{Active: "A", Versions: map[string]string{"A": "0.0.0"}, MinVersion: "0.0.0"}, nil
	}
	var s State
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

func (a *Agent) save(s State) error {
	b, _ := json.MarshalIndent(s, "", " ")
	tmp := a.statePath() + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	f.Write(b)
	f.Sync()
	f.Close()
	return os.Rename(tmp, a.statePath()) // atomic: power loss leaves old or new, never half
}

func other(slot string) string {
	if slot == "A" {
		return "B"
	}
	return "A"
}

// Stage verifies and writes an image to the inactive slot and arms a trial boot.
func (a *Agent) Stage(m Manifest, image []byte) error {
	s, err := a.Load()
	if err != nil {
		return err
	}
	switch {
	case s.Pending != "":
		return ErrBusy
	case !m.VerifySig(a.Pub):
		return ErrSignature
	case m.Device != a.Device:
		return ErrDevice
	case CompareSemver(m.Version, s.MinVersion) <= 0 || CompareSemver(m.Version, s.Versions[s.Active]) <= 0:
		return ErrRollback
	}
	sum := sha256.Sum256(image)
	if hex.EncodeToString(sum[:]) != m.SHA256 || int64(len(image)) != m.Size {
		return ErrDigest
	}
	slot := other(s.Active)
	if err := os.WriteFile(filepath.Join(a.Dir, "slot-"+slot+".img"), image, 0o600); err != nil {
		return err
	}
	s.Pending, s.TriesLeft = slot, a.BootTries
	if s.Versions == nil {
		s.Versions = map[string]string{}
	}
	s.Versions[slot] = m.Version
	return a.save(s)
}

// Boot is what the bootloader hook runs: it decides which slot to boot.
func (a *Agent) Boot() (string, error) {
	s, err := a.Load()
	if err != nil {
		return "", err
	}
	slot := s.Active
	if s.Pending != "" {
		if s.TriesLeft > 0 {
			s.TriesLeft--
			slot = s.Pending
		} else {
			s.Pending = "" // trial failed: fall back
			s.RolledBack++
		}
	}
	s.LastBooted = slot
	return slot, a.save(s)
}

// Confirm is called by the health check after a good boot; it commits the trial slot.
func (a *Agent) Confirm() error {
	s, err := a.Load()
	if err != nil {
		return err
	}
	if s.Pending == "" || s.LastBooted != s.Pending {
		return nil
	}
	s.Active, s.Pending = s.Pending, ""
	s.MinVersion = s.Versions[s.Active] // raise the anti-rollback floor only after success
	return a.save(s)
}

func CompareSemver(a, b string) int {
	pa, pb := strings.SplitN(a, ".", 3), strings.SplitN(b, ".", 3)
	for i := 0; i < 3; i++ {
		x, y := 0, 0
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func (s State) String() string {
	return fmt.Sprintf("active=%s(%s) pending=%q tries=%d floor=%s rollbacks=%d",
		s.Active, s.Versions[s.Active], s.Pending, s.TriesLeft, s.MinVersion, s.RolledBack)
}
