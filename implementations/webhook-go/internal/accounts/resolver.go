// Package accounts resolves an inbound platform identifier (e.g. an UltraMSG
// instanceId, a Twilio number) to a FieldWatch account. V1 resolves from a
// static config map so the inbound path has no runtime dependency on
// comms-channels; an HTTP-backed resolver against the channels lookup endpoint
// can be added later behind the same Resolver interface.
package accounts

import (
	"context"
	"errors"
)

// ErrNotFound means the platform identifier is not registered to any account.
// Callers treat this as a deliberate drop (acknowledge, do not forward).
var ErrNotFound = errors.New("account not found")

// Account is the FieldWatch-internal account an inbound message belongs to.
type Account struct {
	ID string
}

// Resolver maps a (platform, identifier) pair to an Account.
type Resolver interface {
	Resolve(ctx context.Context, platform, identifier string) (Account, error)
}
