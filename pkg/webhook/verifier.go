package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
)

// VerifySHA256Signature calculates the HMAC-SHA256 signature of the body using the
// secret and compares it with the signature (hex-encoded) using subtle.ConstantTimeCompare.
func VerifySHA256Signature(body []byte, signature string, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expectedMAC := mac.Sum(nil)

	actualMAC, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}

	return subtle.ConstantTimeCompare(expectedMAC, actualMAC) == 1
}

// VerifyHeaderSignature extracts signature from header using optional prefix (e.g., "sha256=")
// and verifies it.
func VerifyHeaderSignature(body []byte, headerVal string, secret string, prefix string) bool {
	sig := headerVal
	if prefix != "" {
		if !strings.HasPrefix(sig, prefix) {
			return false
		}
		sig = strings.TrimPrefix(sig, prefix)
	}
	return VerifySHA256Signature(body, sig, secret)
}

// VerifierMiddleware returns an HTTP middleware that reads the request body, validates
// the signature header, restores the body, and aborts with StatusUnauthorized if invalid.
func VerifierMiddleware(secret, headerName, prefix string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if secret == "" {
				next.ServeHTTP(w, r)
				return
			}

			sigHeader := r.Header.Get(headerName)
			if sigHeader == "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"missing signature header"}`))
				return
			}

			// Read body
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"failed to read request body"}`))
				return
			}
			// Restore request body for subsequent handlers
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

			// Verify
			if !VerifyHeaderSignature(bodyBytes, sigHeader, secret, prefix) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"invalid signature"}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
