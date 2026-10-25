package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckPolicyBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		pw      string
		wantErr bool
	}{
		{"11 bytes refused", strings.Repeat("a", 11), true},
		{"12 bytes accepted", strings.Repeat("a", 12), false},
		{"256 bytes accepted", strings.Repeat("a", 256), false},
		{"257 bytes refused", strings.Repeat("a", 257), true},
		{"12 runes of 3 bytes each is 36 bytes: accepted", strings.Repeat("€", 12), false},
		{"11 runes refused even if many bytes", strings.Repeat("€", 11), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckPolicy(tt.pw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckPolicy(len=%d): err=%v, wantErr=%v", len(tt.pw), err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrWeakPassword) {
				t.Fatalf("error should wrap ErrWeakPassword: %v", err)
			}
		})
	}
}

func TestHashRoundTrip(t *testing.T) {
	const pw = "correct horse battery"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, err := VerifyPassword(hash, pw)
	if err != nil || !ok {
		t.Fatalf("VerifyPassword(right): ok=%v err=%v", ok, err)
	}
}

func TestVerifyRejectsWrongPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, err := VerifyPassword(hash, "correct horse batter")
	if err != nil {
		t.Fatalf("VerifyPassword(wrong): unexpected error %v", err)
	}
	if ok {
		t.Fatal("VerifyPassword accepted a wrong password")
	}
}

func TestHashUsesPinnedParameters(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	salt, key, err := parseHash(hash)
	if err != nil {
		t.Fatalf("parseHash of our own output: %v", err)
	}
	if len(salt) != 16 {
		t.Errorf("salt length = %d, want 16", len(salt))
	}
	if len(key) != 32 {
		t.Errorf("key length = %d, want 32", len(key))
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Errorf("hash prefix does not carry pinned params: %s", hash)
	}
}

func TestVerifyMalformedHashIsErrorNotPanic(t *testing.T) {
	bad := []string{
		"",
		"not-a-hash",
		"$argon2i$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=1024,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=65536,t=3,p=2$!!!$AAAA",
		"$argon2id$v=19$m=65536,t=3,p=2$AAAA$AAAA",
		"$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAA",
	}
	for _, h := range bad {
		ok, err := VerifyPassword(h, "anything-long-enough")
		if err == nil {
			t.Errorf("VerifyPassword(%q): want error", h)
		}
		if ok {
			t.Errorf("VerifyPassword(%q): ok=true on malformed hash", h)
		}
	}
}

func TestDummyHashParsesToPinnedShape(t *testing.T) {
	if _, _, err := parseHash(dummyHash); err != nil {
		t.Fatalf("dummyHash must parse so unknown-email login runs a full verify: %v", err)
	}
}
