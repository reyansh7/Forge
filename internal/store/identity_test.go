package store

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateUsername(t *testing.T) {
	if _, err := ValidateUsername("ab"); err == nil {
		t.Fatal("too short")
	}
	if _, err := ValidateUsername("1abc"); err == nil {
		t.Fatal("must start with a letter")
	}
	if _, err := ValidateUsername("ok name"); err == nil {
		t.Fatal("space")
	}
	got, err := ValidateUsername("  Rey_1  ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Rey_1" {
		t.Fatalf("got %q", got)
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("short"); err == nil {
		t.Fatal("too short")
	}
	if err := ValidatePassword("long-enough-password"); err != nil {
		t.Fatal(err)
	}
}

func TestHashAndCheckPassword(t *testing.T) {
	const pw = "long-enough-password"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	if hash == pw || strings.Contains(hash, pw) {
		t.Fatal("hash must not contain the plaintext")
	}
	if err := CheckPassword(hash, pw); err != nil {
		t.Fatal(err)
	}
	if err := CheckPassword(hash, "wrong-password-xx"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong password: %v", err)
	}
}

func TestSessionTokenHashIsNotReversible(t *testing.T) {
	raw, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if raw == hash {
		t.Fatal("raw token must not equal the stored hash")
	}
	if HashSessionToken(raw) != hash {
		t.Fatal("lookup hash must match insert hash")
	}
	if HashSessionToken("other") == hash {
		t.Fatal("different token must not collide")
	}
}
