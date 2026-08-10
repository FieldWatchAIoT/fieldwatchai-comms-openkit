// Package policy implements confidence scoring, the policy gate, and the
// echo-back text generator. All pure functions — no DB, no AI (per the
// comms-hub design: AI never touches the routing/execute path).
package policy

import (
	"fmt"
	"strings"
)

// Action is the policy gate's decision.
type Action string

const (
	ActionExecute  Action = "execute"
	ActionEchoBack Action = "echo_back"
	ActionClarify  Action = "clarify"
)

// Thresholds are the per-channel confidence cutoffs.
type Thresholds struct {
	High   float64
	Medium float64
}

// DefaultThresholds match the spec defaults.
var DefaultThresholds = Thresholds{High: 0.9, Medium: 0.5}

// ScoreInput is the resolved-parse summary the scorer needs.
type ScoreInput struct {
	ShortIDMatch string // exact | levenshtein_1 | levenshtein_2 | none
	HasTarget    bool
	TargetMatch  string // exact | levenshtein_1 | ambiguous | (other)
	HasCommand   bool
	KnownCommand bool
}

// Score returns a confidence in [0,1] from the exactness of resolution.
func Score(in ScoreInput) float64 {
	s := 1.0
	switch in.ShortIDMatch {
	case "exact":
		// no penalty
	case "levenshtein_1":
		s *= 0.7
	case "levenshtein_2":
		s *= 0.5
	default: // none / unknown — unroutable
		return 0.0
	}

	if in.HasTarget {
		switch in.TargetMatch {
		case "exact":
			// no penalty
		case "levenshtein_1":
			s *= 0.6
		case "ambiguous":
			s *= 0.3
		default:
			s *= 0.2
		}
	}

	if in.HasCommand && !in.KnownCommand {
		s *= 0.8
	}
	return s
}

// Decide applies the policy gate. Command-type overrides win over thresholds:
// SOS always executes; group targets and high-impact commands always echo back.
func Decide(confidence float64, command, targetKind string, th Thresholds) Action {
	switch strings.ToUpper(command) {
	case "SOS":
		return ActionExecute
	}
	if targetKind == "group" {
		return ActionEchoBack
	}
	switch strings.ToUpper(command) {
	case "MISSING", "DAMAGE":
		return ActionEchoBack
	}

	switch {
	case confidence >= th.High:
		return ActionExecute
	case confidence >= th.Medium:
		return ActionEchoBack
	default:
		return ActionClarify
	}
}

// EchoText composes the confirmation sent back to the sender.
func EchoText(target, original string, recallSeconds int) string {
	minutes := recallSeconds / 60
	if minutes < 1 {
		minutes = 1
	}
	unit := "minutes"
	if minutes == 1 {
		unit = "minute"
	}
	return fmt.Sprintf("[Forwarded to %s: %s. Reply 'OOPS' within %d %s to recall.]",
		target, original, minutes, unit)
}
