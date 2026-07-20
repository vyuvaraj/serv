package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerifySHA256Signature(t *testing.T) {
	secret := "supersecret"
	body := []byte("hello world")

	// Calculate correct HMAC
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	if !VerifySHA256Signature(body, sig, secret) {
		t.Error("expected signature to be valid")
	}

	if VerifySHA256Signature(body, sig, "wrongsecret") {
		t.Error("expected signature to be invalid with wrong secret")
	}

	if VerifySHA256Signature(body, "invalidsighex", secret) {
		t.Error("expected signature to be invalid with bad hex string")
	}
}

func TestVerifyHeaderSignature(t *testing.T) {
	secret := "mysecret"
	body := []byte("payload data")

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	// Prefix test
	headerVal := "sha256=" + sig
	if !VerifyHeaderSignature(body, headerVal, secret, "sha256=") {
		t.Error("expected prefixed header signature to be valid")
	}

	if VerifyHeaderSignature(body, headerVal, secret, "invalidprefix=") {
		t.Error("expected header signature to be invalid with incorrect prefix")
	}
}

func TestVerifierMiddleware(t *testing.T) {
	secret := "secretkey"
	body := "hello webhook"
	
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	sig := hex.EncodeToString(mac.Sum(nil))

	handler := VerifierMiddleware(secret, "X-Hub-Signature-256", "sha256=")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}),
	)

	// Case 1: Valid signature
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256="+sig)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected StatusOK, got %v", rr.Code)
	}

	// Case 2: Missing signature header
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected StatusUnauthorized, got %v", rr.Code)
	}

	// Case 3: Invalid signature header
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=wrongsig")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected StatusUnauthorized, got %v", rr.Code)
	}
}
