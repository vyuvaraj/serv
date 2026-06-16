package metrics

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

var (
	registry = &MetricsRegistry{
		httpRequests:      make(map[string]int64),
		requestDuration:   make(map[string]float64),
		requestCount:      make(map[string]int64),
		inFlightRequests:  0,
	}
)

type MetricsRegistry struct {
	mu                sync.RWMutex
	httpRequests      map[string]int64   // key: method|path|status
	requestDuration   map[string]float64 // key: method|path
	requestCount      map[string]int64   // key: method|path
	inFlightRequests  int64
}

func IncHTTPRequests(method, path, status string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	key := fmt.Sprintf("%s|%s|%s", method, path, status)
	registry.httpRequests[key]++
}

func IncInFlight() {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.inFlightRequests++
}

func DecInFlight() {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.inFlightRequests > 0 {
		registry.inFlightRequests--
	}
}

func ObserveRequestDuration(method, path string, duration time.Duration) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	key := fmt.Sprintf("%s|%s", method, path)
	registry.requestDuration[key] += duration.Seconds()
	registry.requestCount[key]++
}

func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		registry.mu.RLock()
		defer registry.mu.RUnlock()

		// Write http_requests_total
		fmt.Fprintln(w, "# HELP servstore_http_requests_total Total number of HTTP requests processed.")
		fmt.Fprintln(w, "# TYPE servstore_http_requests_total counter")
		for key, count := range registry.httpRequests {
			var method, path, status string
			_, _ = fmt.Sscanf(key, "%s|%s|%s", &method, &path, &status)
			// Handle custom parsing if Sscanf has issues
			parts := splitKey(key)
			if len(parts) == 3 {
				method, path, status = parts[0], parts[1], parts[2]
			}
			fmt.Fprintf(w, "servstore_http_requests_total{method=\"%s\",path=\"%s\",status=\"%s\"} %d\n", method, path, status, count)
		}

		// Write http_in_flight_requests
		fmt.Fprintln(w, "\n# HELP servstore_http_in_flight_requests Current number of HTTP requests being processed.")
		fmt.Fprintln(w, "# TYPE servstore_http_in_flight_requests gauge")
		fmt.Fprintf(w, "servstore_http_in_flight_requests %d\n", registry.inFlightRequests)

		// Write request durations
		fmt.Fprintln(w, "\n# HELP servstore_http_request_duration_seconds HTTP request latencies in seconds.")
		fmt.Fprintln(w, "# TYPE servstore_http_request_duration_seconds summary")
		for key, sum := range registry.requestDuration {
			parts := splitKey(key)
			if len(parts) == 2 {
				method, path := parts[0], parts[1]
				count := registry.requestCount[key]
				fmt.Fprintf(w, "servstore_http_request_duration_seconds_sum{method=\"%s\",path=\"%s\"} %f\n", method, path, sum)
				fmt.Fprintf(w, "servstore_http_request_duration_seconds_count{method=\"%s\",path=\"%s\"} %d\n", method, path, count)
			}
		}
	})
}

func splitKey(key string) []string {
	var parts []string
	curr := ""
	for i := 0; i < len(key); i++ {
		if key[i] == '|' {
			parts = append(parts, curr)
			curr = ""
		} else {
			curr += string(key[i])
		}
	}
	parts = append(parts, curr)
	return parts
}

func GetMetricsSnapshot() map[string]interface{} {
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	httpReqs := make(map[string]int64)
	for k, v := range registry.httpRequests {
		httpReqs[k] = v
	}

	reqDurations := make(map[string]float64)
	for k, v := range registry.requestDuration {
		reqDurations[k] = v
	}

	reqCounts := make(map[string]int64)
	for k, v := range registry.requestCount {
		reqCounts[k] = v
	}

	return map[string]interface{}{
		"http_requests_total":           httpReqs,
		"http_in_flight_requests":       registry.inFlightRequests,
		"http_request_duration_seconds": reqDurations,
		"http_request_duration_counts":  reqCounts,
	}
}
