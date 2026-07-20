package ServShared_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vyuvaraj/ServShared"
)

const testSecret = "test-secret-key-for-tenant-middleware"

// newTenantRequest builds a request with auth + optional X-Tenant-ID header,
// runs it through AuthMiddleware → TenantMiddleware, and returns the response.
func newTenantRequest(t *testing.T, tokenTenantID, headerTenantID string) *httptest.ResponseRecorder {
	t.Helper()

	// Set SERV_JWT_SECRET BEFORE constructing the handler — AuthMiddleware reads
	// the env var at creation time (not per-request), so the order matters.
	t.Setenv("SERV_JWT_SECRET", testSecret)

	// Generate token with embedded tenantID
	token, err := ServShared.GenerateUserToken(testSecret, "alice", []string{"user"}, tokenTenantID, time.Hour)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if headerTenantID != "" {
		req.Header.Set("X-Tenant-ID", headerTenantID)
	}

	// Chain: AuthMiddleware → TenantMiddleware → echo handler
	handler := ServShared.AuthMiddleware(
		ServShared.TenantMiddleware(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tid := ServShared.GetTenantID(r)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(tid))
			}),
		),
	)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// TestTenantMiddleware_MatchingHeader verifies that a request whose X-Tenant-ID
// matches the JWT tenant_id claim is allowed through.
func TestTenantMiddleware_MatchingHeader(t *testing.T) {
	rr := newTenantRequest(t, "acme", "acme")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); body != "acme" {
		t.Errorf("expected context tenant 'acme', got '%s'", body)
	}
}

// TestTenantMiddleware_MismatchedHeader verifies that a request whose X-Tenant-ID
// does NOT match the JWT tenant_id claim is rejected with 403.
func TestTenantMiddleware_MismatchedHeader(t *testing.T) {
	rr := newTenantRequest(t, "acme", "rival-corp")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestTenantMiddleware_NoHeader verifies that when no X-Tenant-ID header is sent
// but the token contains a tenant claim, the header defaults to "default" which
// does not match the token claim — so the request is rejected with 403.
func TestTenantMiddleware_NoHeader(t *testing.T) {
	// No X-Tenant-ID header → defaults to "default" → 403 because token says "acme"
	rr := newTenantRequest(t, "acme", "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (missing header defaults to 'default' != 'acme'), got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestTenantMiddleware_NoTenantInToken verifies that service tokens (no tenant_id claim)
// are trusted implicitly — the header value is accepted as-is without verification.
func TestTenantMiddleware_NoTenantInToken(t *testing.T) {
	// tenantID="" means no tenant claim in token → any header value passes through
	rr := newTenantRequest(t, "", "any-tenant")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for service token, got %d: %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); body != "any-tenant" {
		t.Errorf("expected context tenant 'any-tenant', got '%s'", body)
	}
}

// TestGetTenantID_Default verifies that GetTenantID returns empty string
// when TenantMiddleware has not run (no value in context).
func TestGetTenantID_Default(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if tid := ServShared.GetTenantID(req); tid != "" {
		t.Errorf("expected empty tenant ID without middleware, got '%s'", tid)
	}
}

func BenchmarkTokenGenerationAndVerification(b *testing.B) {
	secret := "my-perf-test-secret-key-32-chars-long"
	token, err := ServShared.GenerateUserToken(secret, "alice", []string{"user"}, "tenant-a", time.Hour)
	if err != nil {
		b.Fatalf("failed to generate token: %v", err)
	}

	validator := ServShared.NewAuthValidator(secret, "", "")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		claims, err := validator.ValidateToken(token)
		if err != nil {
			b.Fatalf("failed to validate token: %v", err)
		}
		_ = claims
	}
}
