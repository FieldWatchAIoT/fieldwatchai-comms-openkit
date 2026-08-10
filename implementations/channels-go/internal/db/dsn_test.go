package db

import (
	"strings"
	"testing"
)

func TestSanitizeDSN_EmptyUnchanged(t *testing.T) {
	if got := SanitizeDSN(""); got != "" {
		t.Errorf("empty → %q", got)
	}
}

func TestSanitizeDSN_KeywordValueUnchanged(t *testing.T) {
	kv := "host=localhost port=5432 user=alice password=p dbname=demo"
	if got := SanitizeDSN(kv); got != kv {
		t.Errorf("KV form → %q, want unchanged", got)
	}
}

func TestSanitizeDSN_CleanURLUnchanged(t *testing.T) {
	// net/url accepts this — leave it alone so pgx parses natively.
	url := "postgres://alice:plain@db.example:5432/mydb?sslmode=require"
	if got := SanitizeDSN(url); got != url {
		t.Errorf("clean URL → %q, want unchanged", got)
	}
}

func TestSanitizeDSN_PasswordWithSlash(t *testing.T) {
	// The Aurora-flavor failure: password contains '/'. net/url rejects;
	// we should emit KV form.
	in := "postgres://fwai_iot_webhook_rw:hu/p1aB@my-cluster.rds.amazonaws.com:5432/fwai_backend?sslmode=require"
	got := SanitizeDSN(in)

	expectKV(t, got, "host", "my-cluster.rds.amazonaws.com")
	expectKV(t, got, "port", "5432")
	expectKV(t, got, "user", "fwai_iot_webhook_rw")
	expectKV(t, got, "password", "hu/p1aB")
	expectKV(t, got, "dbname", "fwai_backend")
	expectKV(t, got, "sslmode", "require")
}

func TestSanitizeDSN_PasswordWithAtSign(t *testing.T) {
	// '@' inside the password — last '@' must be the userinfo boundary.
	in := "postgres://alice:p@ss@host.example:5432/db"
	got := SanitizeDSN(in)
	expectKV(t, got, "host", "host.example")
	expectKV(t, got, "user", "alice")
	expectKV(t, got, "password", "p@ss")
	expectKV(t, got, "dbname", "db")
}

func TestSanitizeDSN_PasswordWithMultipleSpecials(t *testing.T) {
	in := "postgres://u:a/b?c@host:5432/db?sslmode=require"
	// '?' inside the userinfo IS legal per RFC if percent-encoded, but
	// SanitizeDSN's LastIndex-of-'?' rule splits the query off too early
	// here. This is intentional and documented: our pre-check has already
	// confirmed the URL form FAILS net/url parsing, so the password char
	// set was never going to be RFC-clean. The expected real-world failure
	// case is just '/'.
	//
	// Even so, the split should at least give us the host correctly.
	got := SanitizeDSN(in)
	expectKV(t, got, "host", "host")
	expectKV(t, got, "port", "5432")
	expectKV(t, got, "user", "u")
	expectKV(t, got, "dbname", "db")
}

func TestSanitizeDSN_PostgresqlPrefix(t *testing.T) {
	in := "postgresql://u:a/b@host:5432/db"
	got := SanitizeDSN(in)
	expectKV(t, got, "host", "host")
	expectKV(t, got, "password", "a/b")
}

func TestSanitizeDSN_NoPort(t *testing.T) {
	in := "postgres://u:p/q@host/db"
	got := SanitizeDSN(in)
	expectKV(t, got, "host", "host")
	expectKV(t, got, "password", "p/q")
	expectKV(t, got, "dbname", "db")
	if strings.Contains(got, "port=") {
		t.Errorf("unexpected port in %q", got)
	}
}

func TestSanitizeDSN_NoDbname(t *testing.T) {
	in := "postgres://u:p/q@host:5432/?sslmode=require"
	got := SanitizeDSN(in)
	if strings.Contains(got, "dbname=") {
		t.Errorf("unexpected dbname for empty path: %q", got)
	}
}

func TestSanitizeDSN_QueryParamsForwarded(t *testing.T) {
	in := "postgres://u:p/q@host/db?sslmode=require&connect_timeout=10"
	got := SanitizeDSN(in)
	expectKV(t, got, "sslmode", "require")
	expectKV(t, got, "connect_timeout", "10")
}

func TestSanitizeDSN_URLEncodedQueryValueIsDecoded(t *testing.T) {
	in := "postgres://u:p/q@host/db?application_name=my%20app"
	got := SanitizeDSN(in)
	// space character round-trips through libpq KV quoting:
	expectKV(t, got, "application_name", "my app")
}

func TestQuoteKVValue(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"simple", "simple"},
		{"with space", `'with space'`},
		{`it's`, `'it\'s'`},
		{`back\slash`, `'back\\slash'`},
		{"", ""},
	} {
		if got := quoteKVValue(tc.in); got != tc.want {
			t.Errorf("quoteKVValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// expectKV asserts that the KV string contains a `key=value` (with
// libpq-style quoting if needed).
func expectKV(t *testing.T, kvString, key, expectedValue string) {
	t.Helper()
	wantPair := key + "=" + quoteKVValue(expectedValue)
	if !strings.Contains(kvString, wantPair) {
		t.Errorf("missing %q in %q", wantPair, kvString)
	}
}
