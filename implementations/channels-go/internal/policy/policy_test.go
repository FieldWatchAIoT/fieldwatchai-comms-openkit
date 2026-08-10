package policy

import (
	"math"
	"strings"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestScore(t *testing.T) {
	cases := []struct {
		name string
		in   ScoreInput
		want float64
	}{
		{"exact + known command", ScoreInput{ShortIDMatch: "exact", HasCommand: true, KnownCommand: true}, 1.0},
		{"levenshtein_1", ScoreInput{ShortIDMatch: "levenshtein_1", HasCommand: true, KnownCommand: true}, 0.7},
		{"levenshtein_2", ScoreInput{ShortIDMatch: "levenshtein_2", HasCommand: true, KnownCommand: true}, 0.5},
		{"none is zero", ScoreInput{ShortIDMatch: "none", HasCommand: true, KnownCommand: true}, 0.0},
		{"unknown command downweights", ScoreInput{ShortIDMatch: "exact", HasCommand: true, KnownCommand: false}, 0.8},
		{"target exact", ScoreInput{ShortIDMatch: "exact", HasTarget: true, TargetMatch: "exact"}, 1.0},
		{"target levenshtein_1", ScoreInput{ShortIDMatch: "exact", HasTarget: true, TargetMatch: "levenshtein_1"}, 0.6},
		{"target ambiguous", ScoreInput{ShortIDMatch: "exact", HasTarget: true, TargetMatch: "ambiguous"}, 0.3},
		{"target other", ScoreInput{ShortIDMatch: "exact", HasTarget: true, TargetMatch: "none"}, 0.2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Score(tc.in); !approx(got, tc.want) {
				t.Errorf("Score(%+v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestDecide(t *testing.T) {
	th := DefaultThresholds
	cases := []struct {
		name       string
		confidence float64
		command    string
		targetKind string
		want       Action
	}{
		{"SOS always executes even at low confidence", 0.1, "SOS", "none", ActionExecute},
		{"group target always echoes", 0.99, "STATUS", "group", ActionEchoBack},
		{"MISSING always echoes", 0.99, "MISSING", "none", ActionEchoBack},
		{"DAMAGE always echoes", 0.99, "DAMAGE", "none", ActionEchoBack},
		{"high confidence executes", 0.95, "STATUS", "none", ActionExecute},
		{"medium confidence echoes", 0.6, "STATUS", "none", ActionEchoBack},
		{"low confidence clarifies", 0.3, "STATUS", "none", ActionClarify},
		{"exactly high threshold executes", 0.9, "STATUS", "none", ActionExecute},
		{"exactly medium threshold echoes", 0.5, "STATUS", "none", ActionEchoBack},
		{"lowercase sos still executes", 0.1, "sos", "none", ActionExecute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.confidence, tc.command, tc.targetKind, th); got != tc.want {
				t.Errorf("Decide(%v,%q,%q) = %q, want %q", tc.confidence, tc.command, tc.targetKind, got, tc.want)
			}
		})
	}
}

func TestEchoText(t *testing.T) {
	got := EchoText("Marsh Harbour Shelter", "42 STATUS full", 120)
	for _, want := range []string{"Marsh Harbour Shelter", "42 STATUS full", "OOPS", "2 minute"} {
		if !strings.Contains(got, want) {
			t.Errorf("EchoText missing %q; got %q", want, got)
		}
	}
}
