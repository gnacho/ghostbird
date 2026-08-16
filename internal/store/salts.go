package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// randomSalt genera una sal de 64 caracteres hex (32 bytes), igual que
// TrafficAnalytics (crypto.randomBytes(32).toString('hex')).
func randomSalt() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generar sal: %w", err)
	}
	return hex.EncodeToString(b), nil
}
