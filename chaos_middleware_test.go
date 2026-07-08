package ServShared

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestChaosMiddleware_Latency(t *testing.T) {
	os.Setenv("SERV_CHAOS_LATENCY_MS", "50")
	defer os.Unsetenv("SERV_CHAOS_LATENCY_MS")

	handler := ChaosMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()

	start := time.Now()
	handler.ServeHTTP(rr, req)
	elapsed := time.Since(start)

	if elapsed < 45*time.Millisecond {
		t.Errorf("Expected request to be delayed by at least 45ms, took %v", elapsed)
	}
	if rr.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %v", rr.Code)
	}
}

func TestChaosMiddleware_Drop(t *testing.T) {
	os.Setenv("SERV_CHAOS_DROP_RATE", "1.0")
	defer os.Unsetenv("SERV_CHAOS_DROP_RATE")

	handler := ChaosMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected StatusServiceUnavailable (503) due to 100%% drop rate, got %v", rr.Code)
	}
}
