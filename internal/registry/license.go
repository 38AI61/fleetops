package registry

import (
	"crypto/hmac"
	"crypto/sha256"
	"regexp"
	"strconv"
	"strings"
)

// licenseBody is the normalized license grammar (GF-34).
var licenseBody = regexp.MustCompile(`^[A-Z0-9]{4,32}$`)

// licenseStripper removes the separators a typed license may contain (GF-34).
var licenseStripper = strings.NewReplacer(" ", "", "-", "")

// NormalizeLicense trims, ASCII-uppercases, and removes spaces and hyphens,
// then requires [A-Z0-9]{4,32}. Anything else is ErrValidation.
func NormalizeLicense(raw string) (string, error) {
	s := licenseStripper.Replace(asciiUpper(strings.TrimSpace(raw)))
	if !licenseBody.MatchString(s) {
		return "", ErrValidation
	}
	return s, nil
}

// LicenseHash is HMAC-SHA256(salt, "license:" + fleet_id + ":" + normalized)
// (D-31). The fleet id is part of the message, so one license hashes
// differently in each fleet and the per-fleet unique index still works.
func LicenseHash(salt []byte, fleetID int64, normalized string) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write([]byte("license:" + strconv.FormatInt(fleetID, 10) + ":" + normalized))
	return mac.Sum(nil)
}

// asciiUpper uppercases only ASCII letters; other bytes pass through unchanged.
func asciiUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'a' <= c && c <= 'z' {
			b[i] = c - ('a' - 'A')
		}
	}
	return string(b)
}
