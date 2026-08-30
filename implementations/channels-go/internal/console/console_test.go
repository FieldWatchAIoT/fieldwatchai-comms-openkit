package console

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func get(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/console", nil))
	return rec
}

func TestServesHTML(t *testing.T) {
	rec := get(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "<title>Comms Console</title>") {
		t.Error("page did not render")
	}
}

// The whole point of embedding the page is that it works with no internet. A
// deployment loses its uplink exactly when this console matters most, so any
// external reference is a defect, not a style preference.
func TestPageMakesNoExternalRequests(t *testing.T) {
	body := get(t).Body.String()
	for _, bad := range []string{
		"https://fonts.googleapis.com", "https://fonts.gstatic.com",
		"cdn.", "unpkg.com", "jsdelivr", "googleapis",
	} {
		if strings.Contains(body, bad) {
			t.Errorf("page references external host %q", bad)
		}
	}
	// Catch any src/href pointing off-origin, which is the general form of the
	// same bug.
	for _, m := range regexp.MustCompile(`(?:src|href)\s*=\s*"([^"]+)"`).FindAllStringSubmatch(body, -1) {
		if strings.HasPrefix(m[1], "http://") || strings.HasPrefix(m[1], "https://") || strings.HasPrefix(m[1], "//") {
			t.Errorf("off-origin reference: %q", m[1])
		}
	}
}

// The page holds no data, but it must not become a way to leak the token or
// load third-party code either.
func TestSecurityHeaders(t *testing.T) {
	h := get(t).Header()
	csp := h.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP should deny by default, got %q", csp)
	}
	if !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("CSP should confine fetches to this origin, got %q", csp)
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
}

// Both themes must be fully defined at token level. A colour whose only
// definition sits inside the dark media query renders one theme's text on the
// other theme's ground for viewers whose OS preference is unset.
func TestBothThemesDefineTheSameTokens(t *testing.T) {
	body := get(t).Body.String()
	rootVars := map[string]bool{}
	for _, m := range regexp.MustCompile(`--([a-z0-9-]+)\s*:`).FindAllStringSubmatch(
		body[strings.Index(body, ":root {"):strings.Index(body, "@media (prefers-color-scheme: dark)")], -1) {
		rootVars[m[1]] = true
	}
	if len(rootVars) < 10 {
		t.Fatalf("expected a full light palette on :root, found %d tokens", len(rootVars))
	}
	dark := body[strings.Index(body, `:root[data-theme="dark"]`):]
	for _, m := range regexp.MustCompile(`--([a-z0-9-]+)\s*:`).FindAllStringSubmatch(dark, -1) {
		if !rootVars[m[1]] {
			t.Errorf("token --%s is defined for dark but never on :root", m[1])
		}
	}
}
