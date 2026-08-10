// Package strdist provides string-distance helpers (Levenshtein edit distance
// and American Soundex) used by the address-book resolver and the contacts
// short_id_check. Pure functions, no deps.
package strdist

import "strings"

// Levenshtein returns the edit distance between a and b. Case-sensitive; the
// caller normalizes case if needed.
func Levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// Soundex returns the 4-character American Soundex code for s (letters only).
// Honors the h/w rule (same-coded letters separated by H or W merge) and the
// vowel rule (a vowel resets, so same codes separated by a vowel are kept).
func Soundex(s string) string {
	letters := make([]rune, 0, len(s))
	for _, r := range strings.ToUpper(s) {
		if r >= 'A' && r <= 'Z' {
			letters = append(letters, r)
		}
	}
	if len(letters) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteRune(letters[0])
	prev := code(letters[0])

	for _, r := range letters[1:] {
		if r == 'H' || r == 'W' {
			continue // skip without resetting prev (merge across h/w)
		}
		c := code(r)
		switch c {
		case 0: // vowel or Y: reset so a later same code is re-coded
			prev = 0
		default:
			if c != prev && b.Len() < 4 {
				b.WriteByte('0' + c)
			}
			prev = c
		}
	}

	out := b.String()
	for len(out) < 4 {
		out += "0"
	}
	return out[:4]
}

// code maps a letter to its Soundex digit; 0 means a vowel/Y (no digit).
func code(r rune) byte {
	switch r {
	case 'B', 'F', 'P', 'V':
		return 1
	case 'C', 'G', 'J', 'K', 'Q', 'S', 'X', 'Z':
		return 2
	case 'D', 'T':
		return 3
	case 'L':
		return 4
	case 'M', 'N':
		return 5
	case 'R':
		return 6
	default: // A E I O U Y (H/W handled by caller)
		return 0
	}
}
