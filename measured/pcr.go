// Package measured checks TPM 2.0 PCRs against golden values (measured boot).
//
// Secure boot stops unsigned code from running. Measured boot records what
// DID run into PCRs, so a key sealed to those PCRs (e.g. the disk key or the
// fleet mTLS key) only unseals on an unmodified boot chain. You need both.
package measured

import (
	"bufio"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ParsePCRRead parses `tpm2_pcrread sha256:0,2,4,7` output.
func ParsePCRRead(out string) (map[int]string, error) {
	pcrs := map[int]string{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		idx, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(idx))
		if err != nil {
			continue
		}
		pcrs[n] = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(val), "0x"))
	}
	if len(pcrs) == 0 {
		return nil, fmt.Errorf("no PCR values found")
	}
	return pcrs, nil
}

// Compare returns the PCR indexes that differ from golden.
func Compare(golden, actual map[int]string) []int {
	var bad []int
	for i, g := range golden {
		if actual[i] != strings.ToLower(g) {
			bad = append(bad, i)
		}
	}
	return bad
}

// Read calls tpm2-tools on the device.
func Read(banks string) (map[int]string, error) {
	out, err := exec.Command("tpm2_pcrread", banks).Output()
	if err != nil {
		return nil, err
	}
	return ParsePCRRead(string(out))
}
