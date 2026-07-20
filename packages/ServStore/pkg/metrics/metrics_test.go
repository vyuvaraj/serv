package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsRegistry(t *testing.T) {
	// 1. Trigger metrics events
	IncHTTPRequests("GET", "/test-bucket", "200")
	IncHTTPRequests("GET", "/test-bucket", "200")
	IncHTTPRequests("PUT", "/test-bucket", "201")

	IncInFlight()
	IncInFlight()
	DecInFlight()

	ObserveRequestDuration("GET", "/test-bucket", 150*time.Millisecond)
	ObserveRequestDuration("GET", "/test-bucket", 250*time.Millisecond)

	// 2. Perform mock metrics request
	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()

	Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}

	body := rr.Body.String()

	// 3. Verify format and values
	if !strings.Contains(body, `servstore_http_requests_total{method="GET",path="/test-bucket",status="200"} 2`) {
		t.Error("missing or incorrect GET request total metric")
	}
	if !strings.Contains(body, `servstore_http_requests_total{method="PUT",path="/test-bucket",status="201"} 1`) {
		t.Error("missing or incorrect PUT request total metric")
	}
	if !strings.Contains(body, "servstore_http_in_flight_requests 1") {
		t.Error("incorrect in-flight requests count")
	}
	if !strings.Contains(body, `servstore_http_request_duration_seconds_sum{method="GET",path="/test-bucket"} 0.400000`) {
		t.Error("incorrect request duration sum")
	}
	if !strings.Contains(body, `servstore_http_request_duration_seconds_count{method="GET",path="/test-bucket"} 2`) {
		t.Error("incorrect request duration count")
	}
}
