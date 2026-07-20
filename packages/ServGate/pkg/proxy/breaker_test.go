package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAutomaticCircuitBreakerSLO(t *testing.T) {
	// 1. Setup a backend server that we can dynamically make slow
	var latency time.Duration
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if latency > 0 {
			time.Sleep(latency)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer backend.Close()

	// 2. Setup Gateway with a route to this backend
	routes := []Route{
		{
			Prefix:  "/test",
			Targets: []string{backend.URL},
		},
	}
	handler := NewGatewayHandler(routes, nil, "")

	// 3. Make 5 fast requests (latency 0) to establish p99 below SLO (SLO is 1s)
	latency = 0
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	}

	// Verify circuit is Closed/Allowing requests
	cb := handler.circuitBreakers[backend.URL]
	if cb == nil {
		t.Fatal("expected circuit breaker to be initialized for target")
	}
	if cb.state != StateClosed {
		t.Fatalf("expected state to be Closed, got %d", cb.state)
	}

	// 4. Change latency to 1.5 seconds (exceeding 1s SLO) and make 5 slow requests
	latency = 1500 * time.Millisecond
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}

	// The circuit should now be OPEN!
	if cb.state != StateOpen {
		t.Fatalf("expected circuit breaker state to be Open after SLO breach, got %d", cb.state)
	}

	// 5. Subsequent request should fail immediately with 503 Service Unavailable (ERR_CIRCUIT_OPEN)
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 Service Unavailable, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ERR_CIRCUIT_OPEN") {
		t.Errorf("expected response body to contain ERR_CIRCUIT_OPEN, got: %s", w.Body.String())
	}
}
