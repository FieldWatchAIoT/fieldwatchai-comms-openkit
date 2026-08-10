package accounts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const lookupCacheTTL = 60 * time.Second

// HTTPResolver resolves accounts via comms-channels' GET /v1/accounts/lookup.
//
//   - 404            -> ErrNotFound (caller drops/acks the message)
//   - any other non-2xx or a network/decode failure -> a transient error, so the
//     caller retries rather than dropping (channels being briefly down must not
//     lose inbound messages)
//
// Positive results are cached for a short TTL because the resolver is hit on
// every inbound message for a given instance.
type HTTPResolver struct {
	client    *http.Client
	lookupURL string
	token     string
	ttl       time.Duration
	now       func() time.Time

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	acc Account
	exp time.Time
}

// NewHTTPResolver builds a resolver pointing at the channels base URL (e.g.
// https://api.example.com), authenticating with the internal
// bearer token. A nil client uses http.DefaultClient.
func NewHTTPResolver(baseURL, token string, client *http.Client) *HTTPResolver {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPResolver{
		client:    client,
		lookupURL: strings.TrimRight(baseURL, "/") + "/v1/accounts/lookup",
		token:     token,
		ttl:       lookupCacheTTL,
		now:       time.Now,
		cache:     map[string]cacheEntry{},
	}
}

// Resolve looks up the account for a platform+identifier via channels.
func (r *HTTPResolver) Resolve(ctx context.Context, platform, identifier string) (Account, error) {
	key := platform + "\x00" + identifier
	if acc, ok := r.cached(key); ok {
		return acc, nil
	}

	q := url.Values{"type": {platform}, "identifier": {identifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.lookupURL+"?"+q.Encode(), nil)
	if err != nil {
		return Account{}, fmt.Errorf("build lookup request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.token)

	resp, err := r.client.Do(req)
	if err != nil {
		return Account{}, fmt.Errorf("account lookup: %w", err) // transient (network)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Account{}, ErrNotFound
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		var body struct {
			AccountID string `json:"account_id"`
			TenantID  string `json:"tenant_id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return Account{}, fmt.Errorf("decode lookup response: %w", err) // transient
		}
		if body.AccountID == "" {
			return Account{}, ErrNotFound
		}
		acc := Account{ID: body.AccountID}
		r.store(key, acc)
		return acc, nil
	default:
		return Account{}, fmt.Errorf("account lookup: channels returned HTTP %d", resp.StatusCode) // transient
	}
}

func (r *HTTPResolver) cached(key string) (Account, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.cache[key]
	if !ok || r.now().After(e.exp) {
		return Account{}, false
	}
	return e.acc, true
}

func (r *HTTPResolver) store(key string, acc Account) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[key] = cacheEntry{acc: acc, exp: r.now().Add(r.ttl)}
}
