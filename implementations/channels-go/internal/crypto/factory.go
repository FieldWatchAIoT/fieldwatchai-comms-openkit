package crypto

import (
	"context"
	"fmt"
)

// Encryption mode identifiers (match config.CREDENTIALS_ENCRYPTION values).
const (
	ModeLocalAES = "localaes"
	ModeKMS      = "kms"
)

// New builds the configured Encryptor. localKeyB64 is required for ModeLocalAES;
// region + kmsKeyID for ModeKMS.
func New(ctx context.Context, mode, region, localKeyB64, kmsKeyID string) (Encryptor, error) {
	switch mode {
	case ModeLocalAES:
		return NewLocalAES(localKeyB64)
	case ModeKMS:
		return NewKMS(ctx, region, kmsKeyID)
	default:
		return nil, fmt.Errorf("unknown encryption mode %q", mode)
	}
}
