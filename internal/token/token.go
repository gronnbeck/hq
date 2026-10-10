// Package token generates and hashes hq's bearer tokens.
//
// Tokens carry a prefix naming their kind, so a token pasted in the wrong
// place is easy to spot. The server stores only SHA-256 hashes; the tokens
// are random enough that a slow hash adds nothing.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

const (
	Session = "hqs_" // a logged-in user
	Access  = "hqa_" // created by a user, shared with agents
	Agent   = "hqt_" // temporary, tied to one agent's identity
)

// New returns a fresh token with the given prefix.
func New(prefix string) string {
	b := make([]byte, 32)
	rand.Read(b)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

func Hash(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}
