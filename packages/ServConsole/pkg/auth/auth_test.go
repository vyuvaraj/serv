package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"servconsole/pkg/config"
)

func TestAuthJWTGenerationAndValidation(t *testing.T) {
	config.ActiveDiscovery.JWTSecret = "stable-test-secret"
	Init(func(u, a, m, p string, s int) {})

	username := "admin"
	token, err := GenerateLocalJWT(username)
	if err != nil {
		t.Fatalf("failed to generate JWT: %v", err)
	}

	retUser, role, ok := ValidateJWT(token, JwtSecBytes)
	if !ok {
		t.Fatalf("validation failed")
	}
	if retUser != username {
		t.Errorf("username mismatch: got %q, want %q", retUser, username)
	}
	if role != "admin" {
		t.Errorf("expected role 'admin', got %q", role)
	}
}

func TestAuthJWTInvalidSecret(t *testing.T) {
	config.ActiveDiscovery.JWTSecret = "stable-test-secret"
	Init(func(u, a, m, p string, s int) {})

	username := "developer-bob"
	token, _ := GenerateLocalJWT(username)

	wrongSecret := []byte("incorrect-secret-bytes-key")
	_, _, ok := ValidateJWT(token, wrongSecret)
	if ok {
		t.Error("expected validation to fail with incorrect secret key")
	}
}

func TestBase64UrlCoding(t *testing.T) {
	src := []byte("hello-world-testing-auth-base64")
	encoded := Base64UrlEncode(src)
	decoded, err := Base64UrlDecode(encoded)
	if err != nil {
		t.Fatalf("failed decoding: %v", err)
	}
	if string(decoded) != string(src) {
		t.Errorf("mismatch: got %q, want %q", string(decoded), string(src))
	}
}

func TestAuthorizeConsoleMiddleware(t *testing.T) {
	os.Setenv("SERV_JWT_SECRET", "stable-test-secret")
	defer os.Unsetenv("SERV_JWT_SECRET")

	config.ActiveDiscovery.JWTSecret = "stable-test-secret"
	Init(func(u, a, m, p string, s int) {})

	handlerCalled := false
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := AuthorizeConsole(nextHandler)

	// 1. Test unauthorized request
	req1 := httptest.NewRequest("GET", "/api/routes", nil)
	w1 := httptest.NewRecorder()
	middleware.ServeHTTP(w1, req1)

	if w1.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", w1.Code)
	}
	if handlerCalled {
		t.Error("expected handler to not be called")
	}

	// 2. Test authorized request
	token, _ := GenerateLocalJWT("admin")
	u, r, ok := ValidateJWT(token, JwtSecBytes)
	t.Logf("ValidateJWT result: user=%q, role=%q, ok=%v, token=%q, secret=%q", u, r, ok, token, string(JwtSecBytes))

	req2 := httptest.NewRequest("GET", "/api/routes", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	middleware.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w2.Code)
	}
	if !handlerCalled {
		t.Error("expected handler to be called")
	}
}

func TestGetUserRoleDefaults(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/status", nil)
	role := GetUserRole(req)
	if role != "viewer" {
		t.Errorf("expected default role 'viewer', got %q", role)
	}
}
