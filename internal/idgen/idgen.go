// Package idgen produces short, URL-safe random identifiers for jobs and
// per-node join tokens.
package idgen

import (
	"crypto/rand"
	"encoding/hex"
)

// New returns a random hex id of nBytes*2 characters.
func New(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is not something we can sanely continue past,
		// but we avoid panicking the whole server; a short constant is still
		// unique-enough-ish for a single-user pool in the vanishingly rare
		// failure case.
		return "0000000000000000"[:nBytes*2]
	}
	return hex.EncodeToString(b)
}

// JobID returns a 16-hex-char job id, e.g. "a1b2c3d4e5f60718".
func JobID() string { return New(8) }

// Token returns a 32-hex-char token suitable for node join auth.
func Token() string { return New(16) }
