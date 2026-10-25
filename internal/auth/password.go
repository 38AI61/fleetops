// Package auth holds password hashing, JWT issue/verify, the login throttle,
// refresh-token rotation, and the HTTP handlers and middleware built on them.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters pinned by D-04 (64 MiB, t=3, p=2).
const (
	argonMemoryKiB   uint32 = 64 * 1024
	argonTime        uint32 = 3
	argonThreads     uint8  = 2
	argonSaltBytes          = 16
	argonKeyBytes    uint32 = 32
	MinPasswordRunes        = 12
	MaxPasswordBytes        = 256
)

// ErrWeakPassword means the password breaks the D-04 / GF-27 policy.
var ErrWeakPassword = errors.New("password does not meet policy")

// ErrMalformedHash means a stored hash string is not a pinned argon2id PHC string.
var ErrMalformedHash = errors.New("malformed password hash")

// dummyHash is verified against when the email is unknown, so a miss takes as
// long as a hit (timing equalization). Its plaintext is not a credential.
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=2$kkUZV5UUpf27at+8k1ON2Q$pCNVnNr/KIk8iyrFYVwmKrXgvU/el5hjjxMnS4v6pSk"

// CheckPolicy enforces min 12 runes and max 256 bytes (GF-27). No composition rules.
func CheckPolicy(pw string) error {
	if len(pw) > MaxPasswordBytes {
		return fmt.Errorf("%w: at most %d bytes", ErrWeakPassword, MaxPasswordBytes)
	}
	if utf8.RuneCountInString(pw) < MinPasswordRunes {
		return fmt.Errorf("%w: at least %d characters", ErrWeakPassword, MinPasswordRunes)
	}
	return nil
}

// HashPassword returns a PHC-format argon2id string with the pinned parameters.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, argonSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("salt: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyBytes)
	return encodeHash(salt, key), nil
}

// VerifyPassword reports whether pw matches hash. A malformed hash is an error.
func VerifyPassword(hash, pw string) (bool, error) {
	salt, want, err := parseHash(hash)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(pw), salt, argonTime, argonMemoryKiB, argonThreads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func encodeHash(salt, key []byte) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key))
}

// parseHash accepts only the pinned parameter set; anything else is malformed.
func parseHash(hash string) (salt, key []byte, err error) {
	parts := strings.Split(hash, "$")
	// ["", "argon2id", "v=19", "m=…,t=…,p=…", salt, key]
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return nil, nil, ErrMalformedHash
	}
	if parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return nil, nil, ErrMalformedHash
	}
	wantParams := fmt.Sprintf("m=%d,t=%d,p=%d", argonMemoryKiB, argonTime, argonThreads)
	if parts[3] != wantParams {
		return nil, nil, ErrMalformedHash
	}
	b64 := base64.RawStdEncoding
	salt, err = b64.DecodeString(parts[4])
	if err != nil || len(salt) != argonSaltBytes {
		return nil, nil, ErrMalformedHash
	}
	key, err = b64.DecodeString(parts[5])
	if err != nil || len(key) != int(argonKeyBytes) {
		return nil, nil, ErrMalformedHash
	}
	return salt, key, nil
}
