package auth

import (
	"crypto/rand"
	"testing"
	"time"
)

func TestJWTRoundTrip(t *testing.T) {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)

	claims := JWTClaims{
		Username:  "test-user",
		Role:      "admin",
		ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
		Issuer:    "servstore-test",
	}

	token, err := GenerateToken(claims, secret)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	validated, err := ValidateToken(token, secret)
	if err != nil {
		t.Fatalf("Failed to validate token: %v", err)
	}

	if validated.Username != claims.Username {
		t.Errorf("Expected username %q, got %q", claims.Username, validated.Username)
	}
	if validated.Role != claims.Role {
		t.Errorf("Expected role %q, got %q", claims.Role, validated.Role)
	}
}

func TestJWTExpired(t *testing.T) {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)

	claims := JWTClaims{
		Username:  "expired-user",
		Role:      "user",
		ExpiresAt: time.Now().Add(-1 * time.Second).Unix(), // Expired 1 second ago
		Issuer:    "servstore-test",
	}

	token, err := GenerateToken(claims, secret)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	_, err = ValidateToken(token, secret)
	if err != ErrExpiredToken {
		t.Errorf("Expected ErrExpiredToken, got %v", err)
	}
}

func TestJWTInvalidSignature(t *testing.T) {
	secret1 := []byte("secret-number-one-thirtytwo-bytes")
	secret2 := []byte("secret-number-two-thirtytwo-bytes")

	claims := JWTClaims{
		Username:  "user",
		Role:      "user",
		ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
		Issuer:    "servstore-test",
	}

	token, err := GenerateToken(claims, secret1)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	_, err = ValidateToken(token, secret2)
	if err != ErrInvalidToken {
		t.Errorf("Expected ErrInvalidToken, got %v", err)
	}
}
