package parser

import "testing"

// defaultCfg mirrors a typical channel command set.
func defaultCfg() Config {
	return Config{Commands: []string{"STATUS", "NEEDS", "DAMAGE", "MISSING", "RESOURCE", "HERE", "NOTE", "SOS"}}
}

func TestParse(t *testing.T) {
	cfg := defaultCfg()
	cases := []struct {
		name string
		in   string
		want ParseResult
	}{
		{
			name: "command with payload",
			in:   "42 STATUS full",
			want: ParseResult{OK: true, ShortID: "42", Command: "STATUS", KnownCommand: true, Payload: "full"},
		},
		{
			name: "command multi-word payload",
			in:   "42 NEEDS water 200 ppl",
			want: ParseResult{OK: true, ShortID: "42", Command: "NEEDS", KnownCommand: true, Payload: "water 200 ppl"},
		},
		{
			name: "SOS command",
			in:   "42 SOS structural damage in dorm B",
			want: ParseResult{OK: true, ShortID: "42", Command: "SOS", KnownCommand: true, Payload: "structural damage in dorm B"},
		},
		{
			name: "lowercase command normalizes",
			in:   "42 status full",
			want: ParseResult{OK: true, ShortID: "42", Command: "STATUS", KnownCommand: true, Payload: "full"},
		},
		{
			name: "unknown command still parses",
			in:   "42 hello world",
			want: ParseResult{OK: true, ShortID: "42", Command: "HELLO", KnownCommand: false, Payload: "world"},
		},
		{
			name: "target via to, free-text payload (no command)",
			in:   "42 to @17 hows your space looking",
			want: ParseResult{OK: true, ShortID: "42", HasTarget: true, Target: "17", Payload: "hows your space looking"},
		},
		{
			name: "target via arrow",
			in:   "42 → @abaco we have overflow capacity",
			want: ParseResult{OK: true, ShortID: "42", HasTarget: true, Target: "abaco", Payload: "we have overflow capacity"},
		},
		{
			name: "alpha short id with arrow target",
			in:   "EOC → @42 confirmed, dispatching team",
			want: ParseResult{OK: true, ShortID: "EOC", HasTarget: true, Target: "42", Payload: "confirmed, dispatching team"},
		},
		{
			name: "empty is not ok",
			in:   "   ",
			want: ParseResult{OK: false},
		},
		{
			name: "bare short id is ambiguous",
			in:   "42",
			want: ParseResult{OK: false, ShortID: "42"},
		},
		{
			// Syntactically valid prefix "we"; routability is the resolver's job
			// (it will find no contact "we"). The parser is permissive.
			name: "looks-like-prose still parses syntactically",
			in:   "we need water",
			want: ParseResult{OK: true, ShortID: "we", Command: "NEED", KnownCommand: false, Payload: "water"},
		},
		{
			name: "short id too long is not routable",
			in:   "toolongname STATUS x",
			want: ParseResult{OK: false},
		},
		{
			name: "short id with punctuation is invalid",
			in:   "4!2 STATUS x",
			want: ParseResult{OK: false},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.in, cfg)
			if got != tc.want {
				t.Errorf("Parse(%q)\n got:  %+v\n want: %+v", tc.in, got, tc.want)
			}
		})
	}
}
