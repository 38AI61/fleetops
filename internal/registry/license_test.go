package registry

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

// licenseSalt is 32 bytes, not a real salt.
var licenseSalt = []byte("0123456789abcdef0123456789abcdef")

func TestLicenseNormalization(t *testing.T) {
	cases := []struct {
		name, raw, want string
		wantErr         bool
	}{
		{"spaces and hyphens", " ab-12 cd ", "AB12CD", false},
		{"lowercase", "ab12cd", "AB12CD", false},
		{"minimum length", "ABCD", "ABCD", false},
		{"maximum length", "A1234567890123456789012345678901", "A1234567890123456789012345678901", false},
		{"too short", "ABC", "", true},
		{"too long", "A12345678901234567890123456789012", "", true},
		{"punctuation", "AB.12CD", "", true},
		{"underscore", "AB_12CD", "", true},
		{"empty", "   ", "", true},
		{"non-ascii letter", "ÄB12CD", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeLicense(c.raw)
			if c.wantErr {
				if !errors.Is(err, ErrValidation) {
					t.Fatalf("NormalizeLicense(%q) error = %v, want ErrValidation", c.raw, err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("NormalizeLicense(%q) = (%q, %v), want (%q, nil)", c.raw, got, err, c.want)
			}
		})
	}
}

func TestLicenseHashIsDeterministic(t *testing.T) {
	a := LicenseHash(licenseSalt, 1, "AB12CD")
	b := LicenseHash(licenseSalt, 1, "AB12CD")
	if !bytes.Equal(a, b) {
		t.Fatal("same input produced different hashes")
	}
}

func TestLicenseHashKnownAnswer(t *testing.T) {
	// Vector computed independently with Python's hmac module over "license:1:AB12CD".
	want := "23f8b500f160732e2c16cc17cced60d5da4b8c964d075a4c173946a1788b11a4"
	got := LicenseHash(licenseSalt, 1, "AB12CD")
	if hex.EncodeToString(got) != want {
		t.Fatalf("LicenseHash = %x, want %s", got, want)
	}
}

func TestLicenseHashDiffersAcrossSalts(t *testing.T) {
	other := []byte("ffffffffffffffffffffffffffffffff")
	if bytes.Equal(LicenseHash(licenseSalt, 1, "AB12CD"), LicenseHash(other, 1, "AB12CD")) {
		t.Fatal("different salts produced the same hash")
	}
}

func TestLicenseHashDiffersAcrossFleets(t *testing.T) {
	if bytes.Equal(LicenseHash(licenseSalt, 1, "AB12CD"), LicenseHash(licenseSalt, 2, "AB12CD")) {
		t.Fatal("same license in two fleets produced the same hash")
	}
}
