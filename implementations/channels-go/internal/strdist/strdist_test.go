package strdist

import "testing"

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"42", "42", 0},
		{"42", "43", 1},
		{"42", "4", 1},
		{"HMB", "HMW", 1},
		{"HMB", "XYZ", 3},
		{"", "abc", 3},
		{"kitten", "sitting", 3},
	}
	for _, c := range cases {
		if got := Levenshtein(c.a, c.b); got != c.want {
			t.Errorf("Levenshtein(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSoundex(t *testing.T) {
	// Classic Soundex expectations.
	cases := map[string]string{
		"Robert":    "R163",
		"Rupert":    "R163",
		"Rubin":     "R150",
		"Ashcraft":  "A261",
		"Tymczak":   "T522",
		"":          "",
	}
	for in, want := range cases {
		if got := Soundex(in); got != want {
			t.Errorf("Soundex(%q) = %q, want %q", in, got, want)
		}
	}
	// Homophones should collide.
	if Soundex("Smith") != Soundex("Smyth") {
		t.Errorf("Smith/Smyth should share a Soundex code")
	}
}
