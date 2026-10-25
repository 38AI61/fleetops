package devicekey

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// kat is the known-answer salt: 32 bytes, not a real secret.
var kat = []byte("0123456789abcdef0123456789abcdef")

// katSecret is 43 characters of the base64url alphabet, not a real secret.
const katSecret = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"

func TestFormatParseRoundTrip(t *testing.T) {
	secret, encoded, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	if len(secret) != 32 || len(encoded) != 43 {
		t.Fatalf("secret %d bytes, encoded %d chars; want 32 and 43", len(secret), len(encoded))
	}
	key := Format(42, secret)
	id, got, err := Parse(key)
	if err != nil {
		t.Fatalf("Parse(%q): %v", key, err)
	}
	if id != 42 || got != encoded {
		t.Fatalf("Parse = (%d, %q), want (42, %q)", id, got, encoded)
	}
}

func TestParseSecretWithUnderscoreAndDash(t *testing.T) {
	// 43 characters containing '_' and '-'; the parser must keep them all.
	secret := "_-" + strings.Repeat("aZ9_-", 8) + "x"
	if len(secret) != 43 {
		t.Fatalf("fixture length %d, want 43", len(secret))
	}
	id, got, err := Parse("fo_dev_7_" + secret)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if id != 7 || got != secret {
		t.Fatalf("Parse = (%d, %q), want (7, %q)", id, got, secret)
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	valid := "fo_dev_1_" + katSecret
	cases := map[string]string{ //nolint:gosec // made-up key fixtures, not credentials
		"leading zero id":    "fo_dev_01_" + katSecret,
		"zero id":            "fo_dev_0_" + katSecret,
		"empty secret":       "fo_dev_1_",
		"short secret":       "fo_dev_1_" + katSecret[:42],
		"long secret":        "fo_dev_1_" + katSecret + "x",
		"wrong prefix":       "fo_key_1_" + katSecret,
		"extra segment":      valid + "_extra",
		"non-numeric id":     "fo_dev_x_" + katSecret,
		"bad secret char":    "fo_dev_1_" + katSecret[:42] + "+",
		"empty string":       "",
		"whitespace padding": " " + valid,
		"id overflows int64": "fo_dev_99999999999999999999_" + katSecret,
	}
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := Parse(key)
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("Parse(%q) error = %v, want ErrMalformed", key, err)
			}
		})
	}
}

func TestHashKnownAnswer(t *testing.T) {
	// Vector computed independently with Python's hmac module.
	want := "76958b7c808429bea001c236f45c1243a9de7092b7cb279b24f6b93cff07029a"
	got := Hash(kat, katSecret)
	if hex.EncodeToString(got) != want {
		t.Fatalf("Hash = %x, want %s", got, want)
	}
}

func TestHashLengthIs32(t *testing.T) {
	if n := len(Hash(kat, katSecret)); n != 32 {
		t.Fatalf("Hash length = %d, want 32", n)
	}
}

func TestVerifyAcceptsCorrectSecret(t *testing.T) {
	stored := Hash(kat, katSecret)
	if !Verify(kat, katSecret, stored) {
		t.Fatal("Verify rejected the correct secret")
	}
}

func TestVerifyRejectsOneByteFlippedSecret(t *testing.T) {
	stored := Hash(kat, katSecret)
	flipped := []byte(katSecret)
	flipped[0] ^= 0x01
	if Verify(kat, string(flipped), stored) {
		t.Fatal("Verify accepted a one-byte-flipped secret")
	}
}

func TestVerifyRejectsWrongSalt(t *testing.T) {
	stored := Hash(kat, katSecret)
	other := []byte("ffffffffffffffffffffffffffffffff")
	if Verify(other, katSecret, stored) {
		t.Fatal("Verify accepted a secret hashed under a different salt")
	}
}
