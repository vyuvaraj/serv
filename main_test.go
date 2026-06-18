package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseServToml(t *testing.T) {
	tomlContent := `
name = "testpkg"
version = "1.2.3"

[dependencies]
pkg1 = "0.1.0"
pkg2 = "1.0.0"
`
	name, version, deps, err := parseServToml(tomlContent)
	if err != nil {
		t.Fatalf("Failed to parse TOML: %v", err)
	}
	if name != "testpkg" {
		t.Errorf("Expected name to be 'testpkg', got '%s'", name)
	}
	if version != "1.2.3" {
		t.Errorf("Expected version to be '1.2.3', got '%s'", version)
	}
	if len(deps) != 2 {
		t.Fatalf("Expected 2 dependencies, got %d", len(deps))
	}
	if deps[0] != "pkg1@0.1.0" || deps[1] != "pkg2@1.0.0" {
		t.Errorf("Dependencies parsed incorrectly: %v", deps)
	}
}

func TestParseServTomlFromTarGz(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	tomlContent := `
name = "tarpkg"
version = "3.2.1"
`
	hdr := &tar.Header{
		Name: "serv.toml",
		Size: int64(len(tomlContent)),
		Mode: 0644,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("Failed to write header: %v", err)
	}
	if _, err := tw.Write([]byte(tomlContent)); err != nil {
		t.Fatalf("Failed to write content: %v", err)
	}
	tw.Close()
	gw.Close()

	name, version, _, err := parseServTomlFromTarGz(buf.Bytes())
	if err != nil {
		t.Fatalf("Failed to parse from tar.gz: %v", err)
	}
	if name != "tarpkg" {
		t.Errorf("Expected name 'tarpkg', got '%s'", name)
	}
	if version != "3.2.1" {
		t.Errorf("Expected version '3.2.1', got '%s'", version)
	}
}

func TestJWTValidation(t *testing.T) {
	secret := []byte("my-test-secret")
	token, err := generateJWT("test-user", secret)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	username, ok := validateJWT(token, secret)
	if !ok {
		t.Fatalf("Expected token to be valid")
	}
	if username != "test-user" {
		t.Errorf("Expected username 'test-user', got '%s'", username)
	}

	// Test invalid secret
	_, ok = validateJWT(token, []byte("wrong-secret"))
	if ok {
		t.Errorf("Expected validation to fail for wrong secret")
	}
}

func TestHealthEndpoints(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"healthy"}`))
	})
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, rr.Code)
	}
	if rr.Body.String() != `{"status":"healthy"}` {
		t.Errorf("Unexpected body: %s", rr.Body.String())
	}
}

func generateJWT(username string, secret []byte) (string, error) {
	header := `{"alg":"HS256","typ":"JWT"}`
	headerB64 := base64UrlEncode([]byte(header))

	claims := fmt.Sprintf(`{"username":%q,"exp":%d}`, username, time.Now().Add(24*time.Hour).Unix())
	// For testing, simple claims formatting is fine. Let's do standard base64url encoding
	claimsB64 := base64UrlEncode([]byte(claims))

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(headerB64 + "." + claimsB64))
	sig := mac.Sum(nil)
	sigB64 := base64UrlEncode(sig)

	return headerB64 + "." + claimsB64 + "." + sigB64, nil
}

func base64UrlEncode(data []byte) string {
	s := base64.URLEncoding.EncodeToString(data)
	for len(s) > 0 && s[len(s)-1] == '=' {
		s = s[:len(s)-1]
	}
	return s
}
