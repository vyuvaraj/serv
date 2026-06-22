package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"servmesh/pkg/registry"
	"servmesh/pkg/resilience"
	"github.com/vyuvaraj/ServShared"
)

type MeshTransport struct {
	base        http.RoundTripper
	registryURL string
	
	mu          sync.Mutex
	cache       map[string][]string // service name -> list of addresses
	cacheExpiry map[string]time.Time
	breakers    map[string]*resilience.CircuitBreaker // target -> breaker
	rrIndex     map[string]int
	
	cacheTTL    time.Duration
}

func NewMeshTransport(registryURL string, cacheTTL time.Duration) *MeshTransport {
	return &MeshTransport{
		base:        http.DefaultTransport,
		registryURL: strings.TrimSuffix(registryURL, "/"),
		cache:       make(map[string][]string),
		cacheExpiry: make(map[string]time.Time),
		breakers:    make(map[string]*resilience.CircuitBreaker),
		rrIndex:     make(map[string]int),
		cacheTTL:    cacheTTL,
	}
}

func (t *MeshTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "serv" {
		return t.base.RoundTrip(req)
	}

	serviceName := strings.ToLower(req.URL.Host)
	targets, err := t.resolve(req.Context(), serviceName)
	if err != nil {
		return nil, fmt.Errorf("mesh: failed to resolve service '%s': %w", serviceName, err)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("mesh: no healthy endpoints found for service '%s'", serviceName)
	}

	// Dynamic Retries + Circuit Breaking Loop
	var lastErr error
	maxRetries := 3
	backoff := 50 * time.Millisecond

	// Store request body for retries
	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		req.Body.Close()
	}

	for i := 0; i < maxRetries; i++ {
		target := t.selectTarget(serviceName, targets)
		if target == "" {
			return nil, fmt.Errorf("mesh: all endpoints for '%s' are blocked by circuit breaker", serviceName)
		}

		breaker := t.getBreaker(target)
		if err := breaker.Allow(); err != nil {
			// Skip and try another target immediately if possible
			continue
		}

		// Rewrite URL
		targetURL, err := url.Parse(target)
		if err != nil {
			return nil, fmt.Errorf("mesh: invalid target URL '%s': %w", target, err)
		}

		clonedReq := req.Clone(req.Context())
		clonedReq.URL.Scheme = targetURL.Scheme
		clonedReq.URL.Host = targetURL.Host
		clonedReq.Host = targetURL.Host

		if len(bodyBytes) > 0 {
			clonedReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		// Inject tracing span
		traceparent := req.Header.Get("traceparent")
		span := ServShared.StartSpan(fmt.Sprintf("mesh:call %s %s", req.Method, serviceName), traceparent)
		if span != nil {
			clonedReq.Header.Set("traceparent", fmt.Sprintf("00-%s-%s-01", span.TraceID, span.SpanID))
		}

		resp, err := t.base.RoundTrip(clonedReq)
		
		if span != nil {
			ServShared.EndSpan(span, err, map[string]interface{}{
				"mesh.service": serviceName,
				"mesh.target":  target,
			})
		}

		if err == nil && resp.StatusCode < 500 {
			breaker.Success()
			return resp, nil
		}

		// Handle failure
		breaker.Failure()
		lastErr = err
		if err == nil {
			lastErr = fmt.Errorf("http error status %d", resp.StatusCode)
			resp.Body.Close()
		}

		time.Sleep(backoff)
		backoff *= 2
	}

	return nil, fmt.Errorf("mesh: inter-service request failed after %d attempts: %w", maxRetries, lastErr)
}

func (t *MeshTransport) resolve(ctx context.Context, serviceName string) ([]string, error) {
	t.mu.Lock()
	if exp, ok := t.cacheExpiry[serviceName]; ok && time.Now().Before(exp) {
		targets := t.cache[serviceName]
		t.mu.Unlock()
		return targets, nil
	}
	t.mu.Unlock()

	// Query Control Plane
	resolveURL := fmt.Sprintf("%s/api/resolve/%s", t.registryURL, serviceName)
	req, err := http.NewRequestWithContext(ctx, "GET", resolveURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned status %d", resp.StatusCode)
	}

	var instances []registry.Instance
	if err := json.NewDecoder(resp.Body).Decode(&instances); err != nil {
		return nil, err
	}

	var targets []string
	for _, inst := range instances {
		targets = append(targets, inst.Address)
	}

	t.mu.Lock()
	t.cache[serviceName] = targets
	t.cacheExpiry[serviceName] = time.Now().Add(t.cacheTTL)
	t.mu.Unlock()

	return targets, nil
}

func (t *MeshTransport) selectTarget(serviceName string, targets []string) string {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Filter targets by circuit breaker state
	var available []string
	for _, target := range targets {
		breaker, ok := t.breakers[target]
		if !ok || breaker.Allow() == nil {
			available = append(available, target)
		}
	}

	if len(available) == 0 {
		return ""
	}

	idx := t.rrIndex[serviceName]
	selected := available[idx%len(available)]
	t.rrIndex[serviceName] = (idx + 1) % len(available)
	return selected
}

func (t *MeshTransport) getBreaker(target string) *resilience.CircuitBreaker {
	t.mu.Lock()
	defer t.mu.Unlock()

	breaker, ok := t.breakers[target]
	if !ok {
		// Threshold: 3 errors, Cooldown: 5 seconds
		breaker = resilience.NewCircuitBreaker(3, 5*time.Second)
		t.breakers[target] = breaker
	}
	return breaker
}
