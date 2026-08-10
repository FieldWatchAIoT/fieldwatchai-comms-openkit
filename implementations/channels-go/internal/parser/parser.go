// Package parser implements the rule-based message grammar. It is a pure
// function: no DB, no AI. It extracts structure; whether a short_id actually
// resolves to a contact (routability) is the resolver's job, so the parser is
// deliberately permissive about the short_id token.
//
// Grammar:
//
//	MESSAGE  = SHORT_ID [WS (to|→) WS TARGET] (COMMAND? PAYLOAD?)
//	SHORT_ID = 1–8 alphanumerics, case-insensitive
//	TARGET   = "@" name
//	COMMAND  = first token after the short_id when there is no target
//	PAYLOAD  = the remainder
//
// When a target is present the remainder is treated as free-text payload (a
// message to that target), not a command — matching the documented examples
// like "42 → @abaco we have overflow capacity".
package parser

import (
	"strings"
	"unicode"
)

// Config carries per-channel parser settings (from channels.parser_config).
type Config struct {
	// Commands is the known command set, matched case-insensitively.
	Commands []string
}

// ParseResult is the structured output. OK is false when there is no routable
// prefix at all (empty, non-alphanumeric/oversized first token) or when the
// message is just a bare short_id with nothing following it (ambiguous).
type ParseResult struct {
	OK           bool
	ShortID      string
	HasTarget    bool
	Target       string // name after '@' (contact-vs-group is decided at resolution)
	Command      string // upper-cased; empty when a target is present
	KnownCommand bool
	Payload      string
}

// Parse applies the grammar to text using the channel's command set.
func Parse(text string, cfg Config) ParseResult {
	s := strings.TrimSpace(text)
	if s == "" {
		return ParseResult{OK: false}
	}

	shortID, rest := firstToken(s)
	if !validShortID(shortID) {
		return ParseResult{OK: false}
	}

	rest = strings.TrimSpace(rest)
	if rest == "" {
		// Bare short_id: no command and no target — ambiguous.
		return ParseResult{OK: false, ShortID: shortID}
	}

	res := ParseResult{OK: true, ShortID: shortID}

	// Optional target, introduced by "to" or "→" and followed by "@name".
	lead, afterLead := firstToken(rest)
	if lead == "→" || strings.EqualFold(lead, "to") {
		tgt, afterTgt := firstToken(strings.TrimSpace(afterLead))
		if strings.HasPrefix(tgt, "@") && len(tgt) > 1 {
			res.HasTarget = true
			res.Target = strings.TrimPrefix(tgt, "@")
			res.Payload = strings.TrimSpace(afterTgt)
			return res
		}
		// Arrow not followed by a target: fall through and parse normally.
	}

	// No target: first token is the command, remainder is payload.
	cmd, afterCmd := firstToken(rest)
	res.Command = strings.ToUpper(cmd)
	res.KnownCommand = containsFold(cfg.Commands, cmd)
	res.Payload = strings.TrimSpace(afterCmd)
	return res
}

// firstToken splits s at the first run of whitespace, returning the leading
// token and the remainder (leading whitespace of the remainder preserved for
// the caller to trim).
func firstToken(s string) (tok, rest string) {
	i := strings.IndexFunc(s, unicode.IsSpace)
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i:]
}

func validShortID(s string) bool {
	n := 0
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
		n++
	}
	return n >= 1 && n <= 8
}

func containsFold(set []string, v string) bool {
	for _, s := range set {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}
