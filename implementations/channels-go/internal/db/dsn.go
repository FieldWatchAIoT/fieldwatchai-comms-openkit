package db

// SanitizeDSN and its helpers are copied verbatim from
// fieldwatchai-iot-webhook (internal/db/dsn.go) to keep Aurora DSN handling
// identical across FieldWatch Go services. Keep in sync with that source.

import (
	"net/url"
	"strings"
)

// SanitizeDSN converts a postgres URL DSN whose password contains characters
// net/url rejects in userinfo (most commonly '/') into pgx's keyword/value
// connection-string form, which has no userinfo encoding constraints.
//
// pgx.ParseConfig accepts both URL form
//
//	postgres://user:password@host:port/dbname?sslmode=require
//
// and keyword/value form
//
//	host=... port=... user=... password=... dbname=... sslmode=require
//
// Aurora's auto-generated passwords frequently contain `/`, which net/url
// rejects in the userinfo segment ("net/url: invalid userinfo"). We can't
// fix it by URL-encoding here without re-implementing url.UserPassword
// behavior, so we convert the whole DSN to keyword/value instead.
//
// Behavior:
//   - empty input → empty output
//   - already keyword/value (no leading "postgres://" or "postgresql://") → unchanged
//   - URL with a userinfo password that net/url accepts → unchanged (pgx parses it)
//   - URL whose password net/url rejects → keyword/value equivalent
func SanitizeDSN(raw string) string {
	if raw == "" {
		return raw
	}
	if !strings.HasPrefix(raw, "postgres://") && !strings.HasPrefix(raw, "postgresql://") {
		return raw
	}
	// Pre-check: leave the URL alone only if net/url parses it AND there's
	// at most one '@' in the userinfo+host portion. Multiple '@' (a password
	// containing '@') makes net/url's optimistic first-'@' split silently
	// give the wrong host — pgx then connects to the wrong place. Converting
	// to KV form sidesteps that.
	if _, err := url.Parse(raw); err == nil && atCountBeforePath(raw) <= 1 {
		return raw
	}
	return urlToKV(raw)
}

// atCountBeforePath counts '@' chars between "://" and the first '/' that
// follows it (i.e. inside the userinfo+host segment).
func atCountBeforePath(raw string) int {
	i := strings.Index(raw, "://")
	if i < 0 {
		return 0
	}
	rest := raw[i+3:]
	if slash := strings.Index(rest, "/"); slash >= 0 {
		rest = rest[:slash]
	}
	return strings.Count(rest, "@")
}

// urlToKV decomposes a postgres URL by hand and emits keyword/value form.
// The parsing rules are intentionally simple:
//
//   - everything before "://" is the scheme (discarded; pgx infers postgres)
//   - everything after the last '?' is the query string
//   - everything after the first '/' (in the remainder) up to '?' is the
//     dbname
//   - within the userinfo + host portion, the LAST '@' separates userinfo
//     from host (passwords may legally contain '@', but hostnames cannot)
//   - within the userinfo, the FIRST ':' separates user from password
//   - within the host portion, the LAST ':' separates host from port
//     (host may be an IPv6 address bracketed with '[]', not supported here)
//
// Caller is responsible for making sure the input is one of the two
// scheme prefixes — SanitizeDSN does that pre-check.
func urlToKV(raw string) string {
	rest := strings.TrimPrefix(raw, "postgresql://")
	if rest == raw {
		rest = strings.TrimPrefix(raw, "postgres://")
	}

	// Split off the query string first; '?' inside a userinfo password is
	// already invalid per net/url so we'd never see it survive a real DSN.
	var rawQuery string
	if i := strings.LastIndex(rest, "?"); i >= 0 {
		rawQuery = rest[i+1:]
		rest = rest[:i]
	}

	// Split off the path (dbname). The FIRST '/' that comes after the
	// host is the boundary — but since '/' can appear in the password, we
	// need to find the '/' that comes after the last '@'.
	var dbname string
	atIdx := strings.LastIndex(rest, "@")
	hostStart := 0
	if atIdx >= 0 {
		hostStart = atIdx + 1
	}
	if slash := strings.Index(rest[hostStart:], "/"); slash >= 0 {
		dbname = rest[hostStart+slash+1:]
		rest = rest[:hostStart+slash]
	}

	// Now rest is userinfo@host[:port]. Re-locate the last '@'.
	var user, password string
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		userinfo := rest[:at]
		rest = rest[at+1:]
		if c := strings.Index(userinfo, ":"); c >= 0 {
			user = userinfo[:c]
			password = userinfo[c+1:]
		} else {
			user = userinfo
		}
	}

	// rest is host[:port].
	host := rest
	port := ""
	if c := strings.LastIndex(rest, ":"); c >= 0 {
		host = rest[:c]
		port = rest[c+1:]
	}

	parts := make([]string, 0, 6)
	add := func(k, v string) {
		if v == "" {
			return
		}
		parts = append(parts, k+"="+quoteKVValue(v))
	}
	add("host", host)
	add("port", port)
	add("user", user)
	add("password", password)
	add("dbname", dbname)

	if rawQuery != "" {
		for _, kv := range strings.Split(rawQuery, "&") {
			eq := strings.Index(kv, "=")
			if eq < 0 {
				continue
			}
			// URL-decode the value side; query params are already permitted
			// to be percent-encoded.
			decoded, err := url.QueryUnescape(kv[eq+1:])
			if err != nil {
				decoded = kv[eq+1:]
			}
			add(kv[:eq], decoded)
		}
	}

	return strings.Join(parts, " ")
}

// quoteKVValue applies the libpq quoting rules pgx expects for KV-form
// values: wrap in single quotes if the value contains a space, single
// quote, or backslash, and escape the latter two with a backslash.
// Values without those characters are returned unchanged.
func quoteKVValue(v string) string {
	if !strings.ContainsAny(v, " '\\") {
		return v
	}
	var b strings.Builder
	b.Grow(len(v) + 4)
	b.WriteByte('\'')
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == '\'' || c == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('\'')
	return b.String()
}
