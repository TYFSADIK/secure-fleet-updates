// Package campaign rolls an update across the fleet in waves and halts automatically.
package campaign

import "fmt"

type Result int

const (
	Pending Result = iota
	Success
	RolledBack
	Unreachable
)

type Wave struct {
	Name     string
	Fraction float64 // cumulative fraction of fleet after this wave
}

var DefaultWaves = []Wave{{"canary", 0.05}, {"early", 0.25}, {"half", 0.5}, {"all", 1.0}}

type Report struct {
	Wave    string
	Updated int
	Failed  int
	Halted  bool
	Reason  string
}

// Run updates devices wave by wave via `update(id) Result`.
// It halts if the failure ratio within a wave exceeds maxFail. Unreachable devices
// (DDIL!) are not failures; they are retried in the next campaign.
func Run(devices []string, waves []Wave, maxFail float64, update func(string) Result) []Report {
	var out []Report
	done := 0
	for _, w := range waves {
		target := int(float64(len(devices))*w.Fraction + 0.5)
		if target < 1 {
			target = 1
		}
		r := Report{Wave: w.Name}
		attempted := 0
		for ; done < target && done < len(devices); done++ {
			switch update(devices[done]) {
			case Success:
				r.Updated++
				attempted++
			case RolledBack:
				r.Failed++
				attempted++
			}
		}
		if attempted > 0 && float64(r.Failed)/float64(attempted) > maxFail {
			r.Halted = true
			r.Reason = fmt.Sprintf("%d/%d rolled back in %s wave", r.Failed, attempted, w.Name)
			out = append(out, r)
			return out
		}
		out = append(out, r)
	}
	return out
}
