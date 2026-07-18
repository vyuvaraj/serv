//go:build !wasm

package runtime

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// JWTSign generates an HS256 signed JWT token from a map payload and a secret.
func JWTSign(payload, secret interface{}) interface{} {
	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}

	headerB64 := base64.RawURLEncoding.EncodeToString(headerBytes)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)

	signingInput := headerB64 + "." + payloadB64
	mac := hmac.New(sha256.New, []byte(toString(secret)))
	mac.Write([]byte(signingInput))
	signatureBytes := mac.Sum(nil)
	signatureB64 := base64.RawURLEncoding.EncodeToString(signatureBytes)

	return signingInput + "." + signatureB64
}

// JWTVerify verifies the token signature and expiration, returning claims if successful.
func JWTVerify(token, secret interface{}) interface{} {
	tStr := toString(token)
	sStr := toString(secret)

	parts := strings.Split(tStr, ".")
	if len(parts) != 3 {
		return [2]interface{}{nil, "invalid JWT token format"}
	}

	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return [2]interface{}{nil, "failed to decode claims"}
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return [2]interface{}{nil, "failed to parse claims JSON"}
	}

	// Expiration check
	if expVal, exists := claims["exp"]; exists {
		var expTime time.Time
		switch v := expVal.(type) {
		case float64:
			expTime = time.Unix(int64(v), 0)
		case int64:
			expTime = time.Unix(v, 0)
		}
		if !expTime.IsZero() && time.Now().After(expTime) {
			return [2]interface{}{nil, "token has expired"}
		}
	}

	// Verify HMAC-SHA256 signature
	mac := hmac.New(sha256.New, []byte(sStr))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	expectedSig := mac.Sum(nil)

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return [2]interface{}{nil, "invalid signature encoding"}
	}

	if !hmac.Equal(sig, expectedSig) {
		return [2]interface{}{nil, "invalid token signature"}
	}

	return claims
}

// JWTDecode decodes a JWT token without verifying its signature, returning claims.
func JWTDecode(token interface{}) interface{} {
	parts := strings.Split(toString(token), ".")
	if len(parts) != 3 {
		return [2]interface{}{nil, "invalid JWT token format"}
	}

	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return [2]interface{}{nil, "failed to decode claims"}
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return [2]interface{}{nil, "failed to parse claims JSON"}
	}

	return claims
}
