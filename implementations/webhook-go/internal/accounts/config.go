package accounts

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ConfigResolver resolves accounts from a static in-memory map, loaded from the
// ACCOUNTS_MAP env var (JSON): platform -> identifier -> account_id, e.g.
//
//	{"whatsapp-ultramsg":{"<instanceId>":"acc_..."}}
type ConfigResolver struct {
	m map[string]map[string]string
}

// NewConfigResolver parses the ACCOUNTS_MAP JSON. An empty string yields a
// valid resolver that resolves nothing (useful before any account is
// configured); malformed JSON is a startup error.
func NewConfigResolver(jsonStr string) (*ConfigResolver, error) {
	m := map[string]map[string]string{}
	if s := strings.TrimSpace(jsonStr); s != "" {
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			return nil, fmt.Errorf("parse ACCOUNTS_MAP: %w", err)
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
