package accounts

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ConfigResolver resolves accounts from a static in-memory JSON map:
// platform -> identifier -> account_id, e.g.
//
//	{"whatsapp-ultramsg":{"<instanceId>":"acc_..."}}
//
// The server does not use this. Account ownership lives in comms-channels, and
// cmd/server always resolves through HTTPResolver against its lookup endpoint —
// a second, divergent source of truth for account routing is exactly the kind
// of thing that drops real messages. This exists as a Resolver implementation
// for tests, which need to resolve without standing up channels.
type ConfigResolver struct {
	m map[string]map[string]string
}

// NewConfigResolver parses the JSON map. An empty string yields a valid
// resolver that resolves nothing; malformed JSON is an error.
func NewConfigResolver(jsonStr string) (*ConfigResolver, error) {
	m := map[string]map[string]string{}
	if s := strings.TrimSpace(jsonStr); s != "" {
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			return nil, fmt.Errorf("parse accounts map: %w", err)
		}
	}
	return &ConfigResolver{m: m}, nil
}

// Resolve returns the account for a platform+identifier, or ErrNotFound.
func (r *ConfigResolver) Resolve(_ context.Context, platform, identifier string) (Account, error) {
	if sub, ok := r.m[platform]; ok {
		if id, ok := sub[identifier]; ok {
			return Account{ID: id}, nil
		}
	}
	return Account{}, ErrNotFound
}
