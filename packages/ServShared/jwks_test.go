package ServShared_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vyuvaraj/ServShared"
)

func TestJWKSVerification(t *testing.T) {
	// 1. Generate RSA key pair for signing
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key pair: %v", err)
	}

	kid := "test-key-id"

	// 2. Start a mock JWKS server
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nStr := base64.RawURLEncoding.EncodeToString(privKey.PublicKey.N.Bytes())
		eBytes := big.NewInt(int64(privKey.PublicKey.E)).Bytes()
		eStr := base64.RawURLEncoding.EncodeToString(eBytes)

		jwks := map[string]interface{}{
			"keys": []map[string]interface{}{
				{
					"kty": "RSA",
					"use": "sig",
					"alg": "RS256",
					"kid": kid,
					"n":   nStr,
					"e":   eStr,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksServer.Close()

	// 3. Generate an RS256 token signed by the private key
	token, err := ServShared.GenerateUserTokenRS256(privKey, kid, "bob", []string{"user"}, "tenant123", time.Hour)
	if err != nil {
		t.Fatalf("failed to generate RS256 token: %v", err)
	}

	// 4. Configure env for AuthMiddleware
	t.Setenv("SERV_JWKS_URL", jwksServer.URL)
	t.Setenv("SERV_JWT_SECRET", "") // Unset HS256 secret

	// 5. Build handler chain
	handler := ServShared.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ServShared.GetClaims(r)
		if claims == nil {
			http.Error(w, "no claims", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(claims.Username))
	}))

	// 6. Execute request
	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	if body := rr.Body.String(); body != "bob" {
		t.Errorf("expected username 'bob', got %q", body)
	}
}
