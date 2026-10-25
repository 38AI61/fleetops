// Package devicekey builds, parses, hashes, and verifies device API keys (D-29,
// GF-31, GF-32). A key is fo_dev_<device_id>_<secret>. Only the HMAC of the
// secret is stored; the plaintext key is shown once by the caller.
package devicekey

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"strconv"
)

// ErrMalformed is returned for any key that does not match the GF-31 grammar.
// It carries no detail, so callers cannot learn which part was wrong.
var ErrMalformed = errors.New("malformed device key")

// secretBytes is the random length of a device secret (GF-31).
const secretBytes = 32

var keyPattern = regexp.MustCompile(`^fo_dev_([1-9][0-9]*)_([A-Za-z0-9_-]{43})$`)

// GenerateSecret returns 32 random bytes and their base64url (no padding) form.
// The device id is not known here; the caller passes the secret to Format once
// the device row exists.
func GenerateSecret() ([]byte, string, error) {
	secret := make([]byte, secretBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, "", err
	}
	return secret, base64.RawURLEncoding.EncodeToString(secret), nil
}

// Format returns the key for deviceID and secret. It is the pure form of key
// construction; GenerateSecret supplies the secret.
func Format(deviceID int64, secret []byte) string {
	return "fo_dev_" + strconv.FormatInt(deviceID, 10) + "_" + base64.RawURLEncoding.EncodeToString(secret)
}

// Parse splits a key into its device id and encoded secret. Any mismatch
// returns ErrMalformed.
func Parse(key string) (int64, string, error) {
	m := keyPattern.FindStringSubmatch(key)
	if m == nil {
		return 0, "", ErrMalformed
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, "", ErrMalformed
	}
	return id, m[2], nil
}

// Hash returns HMAC-SHA256(salt, secret) over the encoded secret (D-29).
func Hash(salt []byte, secret string) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write([]byte(secret))
	return mac.Sum(nil)
}

// Verify reports whether secret hashes to stored under salt, in constant time.
func Verify(salt []byte, secret string, stored []byte) bool {
	return hmac.Equal(Hash(salt, secret), stored)
}
