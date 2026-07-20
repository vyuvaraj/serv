package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("token has expired")
)

type JWTClaims struct {
	Username  string `json:"sub"`
	Role      string `json:"role"`
	ExpiresAt int64  `json:"exp"`
	Issuer    string `json:"iss"`
}

// base64UrlEncode encodes a byte slice into a Base64URL string (no padding).
func base64UrlEncode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// base64UrlDecode decodes a Base64URL string (no padding).
func base64UrlDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// GenerateToken generates an HMAC-SHA256 signed JWT token.
func GenerateToken(claims JWTClaims, secret []byte) (string, error) {
	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}

	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", err
	}

	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	headerB64 := base64UrlEncode(headerBytes)
	payloadB64 := base64UrlEncode(payloadBytes)

	signingInput := headerB64 + "." + payloadB64

	h := hmac.New(sha256.New, secret)
	h.Write([]byte(signingInput))
	signature := base64UrlEncode(h.Sum(nil))

	return signingInput + "." + signature, nil
}

// ValidateToken parses, validates the signature, and checks the expiration of a JWT token.
func ValidateToken(tokenStr string, secret []byte) (*JWTClaims, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}

	headerB64, payloadB64, signatureB64 := parts[0], parts[1], parts[2]

	// Verify signature
	signingInput := headerB64 + "." + payloadB64
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(signingInput))
	expectedSignature := h.Sum(nil)

	actualSignature, err := base64UrlDecode(signatureB64)
	if err != nil {
		return nil, ErrInvalidToken
	}

	if !hmac.Equal(expectedSignature, actualSignature) {
		return nil, ErrInvalidToken
	}

	// Decode claims
	payloadBytes, err := base64UrlDecode(payloadB64)
	if err != nil {
		return nil, ErrInvalidToken
	}

	var claims JWTClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, ErrInvalidToken
	}

	// Verify expiration
	if claims.ExpiresAt < time.Now().Unix() {
		return nil, ErrExpiredToken
	}

	return &claims, nil
}
