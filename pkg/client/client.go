package client

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	mrand "math/rand"
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
	cache       map[string][]registry.Instance // service name -> list of instances
	cacheExpiry map[string]time.Time
	breakers    map[string]*resilience.CircuitBreaker // target -> breaker
	rrIndex     map[string]int
	rules       map[string]registry.RoutingRule
	rulesExpiry map[string]time.Time
	
	cacheTTL    time.Duration
	tlsConfig   *tls.Config
}

func NewMeshTransport(registryURL string, cacheTTL time.Duration) *MeshTransport {
	transport := &http.Transport{
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &MeshTransport{
		base:        transport,
		registryURL: strings.TrimSuffix(registryURL, "/"),
		cache:       make(map[string][]registry.Instance),
		cacheExpiry: make(map[string]time.Time),
		breakers:    make(map[string]*resilience.CircuitBreaker),
		rrIndex:     make(map[string]int),
		rules:       make(map[string]registry.RoutingRule),
		rulesExpiry: make(map[string]time.Time),
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

	// Fetch dynamic routing rules
	rule, _ := t.ResolveRule(req.Context(), serviceName)
	maxRetries := rule.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	backoffVal := rule.BackoffMs
	if backoffVal <= 0 {
		backoffVal = 50
	}
	backoff := time.Duration(backoffVal) * time.Millisecond

	// Dynamic Retries + Circuit Breaking Loop
	var lastErr error

	// Fault Injection (Chaos Engineering)
	if rule.FaultDelayMs > 0 && mrand.Float64() < rule.FaultDelayRatio {
		time.Sleep(time.Duration(rule.FaultDelayMs) * time.Millisecond)
	}
	if rule.FaultErrorStatus > 0 && mrand.Float64() < rule.FaultErrorRatio {
		return &http.Response{
			StatusCode: rule.FaultErrorStatus,
			Body:       io.NopCloser(strings.NewReader("Chaos engineering fault injected")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}

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

		var cancel context.CancelFunc
		ctx := req.Context()
		if rule.TimeoutMs > 0 {
			ctx, cancel = context.WithTimeout(ctx, time.Duration(rule.TimeoutMs)*time.Millisecond)
		}
		clonedReq := req.Clone(ctx)
		if t.tlsConfig != nil {
			clonedReq.URL.Scheme = "https"
		} else {
			clonedReq.URL.Scheme = targetURL.Scheme
		}
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
		if cancel != nil && (err != nil || (resp != nil && resp.StatusCode >= 500)) {
			cancel()
		}
		
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
		oldState := breaker.State()
		breaker.Failure()
		if oldState != resilience.Open && breaker.State() == resilience.Open {
			fmt.Printf("[CIRCUIT_BREAKER] State changed to Open for target %s (threshold: 3 errors)\n", target)
		}
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

func (t *MeshTransport) resolve(ctx context.Context, serviceName string) ([]registry.Instance, error) {
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

	t.mu.Lock()
	t.cache[serviceName] = instances
	t.cacheExpiry[serviceName] = time.Now().Add(t.cacheTTL)
	t.mu.Unlock()

	return instances, nil
}

func (t *MeshTransport) selectTarget(serviceName string, targets []registry.Instance) string {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Filter targets by circuit breaker state
	var available []registry.Instance
	for _, inst := range targets {
		breaker, ok := t.breakers[inst.Address]
		if !ok || breaker.Allow() == nil {
			available = append(available, inst)
		}
	}

	if len(available) == 0 {
		return ""
	}

	// Calculate sum of weights and check if they are all default
	totalWeight := 0
	allDefault := true
	for _, inst := range available {
		w := inst.Weight
		if w <= 0 {
			w = 100
		} else {
			allDefault = false
		}
		totalWeight += w
	}

	if allDefault {
		idx := t.rrIndex[serviceName]
		selected := available[idx%len(available)].Address
		t.rrIndex[serviceName] = (idx + 1) % len(available)
		return selected
	}

	// Perform weighted selection
	val := mrand.Intn(totalWeight)
	current := 0
	for _, inst := range available {
		w := inst.Weight
		if w <= 0 {
			w = 100
		}
		current += w
		if val < current {
			return inst.Address
		}
	}

	return available[0].Address
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

func (t *MeshTransport) renewCertificate(ctx context.Context, serviceName string, jwtToken string) (tls.Certificate, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("failed to generate key: %w", err)
	}

	subj := pkix.Name{
		CommonName:   serviceName + ".servverse",
		Organization: []string{"Servverse"},
	}
	template := &x509.CertificateRequest{
		Subject:            subj,
		SignatureAlgorithm: x509.ECDSAWithSHA256,
		DNSNames:           []string{serviceName, serviceName + ".servverse", "localhost"},
	}

	csrBytes, err := x509.CreateCertificateRequest(rand.Reader, template, priv)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("failed to create CSR: %w", err)
	}

	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrBytes})

	payload := map[string]string{
		"service": serviceName,
		"csr":     string(csrPEM),
	}
	bodyBytes, _ := json.Marshal(payload)

	csrURL := fmt.Sprintf("%s/api/csr", t.registryURL)
	req, err := http.NewRequestWithContext(ctx, "POST", csrURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if jwtToken != "" {
		req.Header.Set("Authorization", "Bearer "+jwtToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("CSR request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return tls.Certificate{}, nil, fmt.Errorf("CSR signing failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var respData struct {
		Certificate string `json:"certificate"`
		CA          string `json:"ca"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		return tls.Certificate{}, nil, err
	}

	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	tlsCert, err := tls.X509KeyPair([]byte(respData.Certificate), keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("failed to parse X509 key pair: %w", err)
	}

	return tlsCert, []byte(respData.CA), nil
}

// SetupmTLS generates keys, CSR, requests signed cert from registry, and configures TLS configs.
func (t *MeshTransport) SetupmTLS(ctx context.Context, serviceName string, jwtToken string) (*tls.Config, *tls.Config, error) {
	tlsCert, caBytes, err := t.renewCertificate(ctx, serviceName, jwtToken)
	if err != nil {
		return nil, nil, err
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caBytes) {
		return nil, nil, fmt.Errorf("failed to append CA cert")
	}

	var activeCert tls.Certificate = tlsCert
	var activeCertMu sync.RWMutex

	clientTLSConfig := &tls.Config{
		Certificates:       []tls.Certificate{tlsCert},
		GetClientCertificate: func(info *tls.CertificateRequestInfo) (*tls.Certificate, error) {
			activeCertMu.RLock()
			defer activeCertMu.RUnlock()
			return &activeCert, nil
		},
		RootCAs:            caPool,
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			opts := x509.VerifyOptions{
				DNSName:       serviceName + ".servverse",
				Intermediates: x509.NewCertPool(),
				Roots:         caPool,
				KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			}
			for _, cert := range cs.PeerCertificates[1:] {
				opts.Intermediates.AddCert(cert)
			}
			_, err := cs.PeerCertificates[0].Verify(opts)
			return err
		},
	}

	serverTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		GetCertificate: func(info *tls.ClientHelloInfo) (*tls.Certificate, error) {
			activeCertMu.RLock()
			defer activeCertMu.RUnlock()
			return &activeCert, nil
		},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
	}

	t.tlsConfig = clientTLSConfig
	if transport, ok := t.base.(*http.Transport); ok {
		transport.TLSClientConfig = clientTLSConfig
	}

	// Dynamic rotation schedule background loop
	go func() {
		ticker := time.NewTicker(300 * time.Millisecond) // Fast ticker for tests/simulation
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				newCert, _, err := t.renewCertificate(ctx, serviceName, jwtToken)
				if err == nil {
					activeCertMu.Lock()
					activeCert = newCert
					activeCertMu.Unlock()
				}
			}
		}
	}()

	return clientTLSConfig, serverTLSConfig, nil
}

func (t *MeshTransport) ResolveRule(ctx context.Context, serviceName string) (registry.RoutingRule, error) {
	t.mu.Lock()
	if exp, ok := t.rulesExpiry[serviceName]; ok && time.Now().Before(exp) {
		rule := t.rules[serviceName]
		t.mu.Unlock()
		return rule, nil
	}
	t.mu.Unlock()

	// Fallback default rule
	defaultRule := registry.RoutingRule{
		Service:    serviceName,
		MaxRetries: 3,
		TimeoutMs:  2000,
		BackoffMs:  50,
	}

	resolveURL := fmt.Sprintf("%s/api/rules/%s", t.registryURL, serviceName)
	req, err := http.NewRequestWithContext(ctx, "GET", resolveURL, nil)
	if err != nil {
		return defaultRule, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return defaultRule, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return defaultRule, fmt.Errorf("registry returned status %d", resp.StatusCode)
	}

	var rule registry.RoutingRule
	if err := json.NewDecoder(resp.Body).Decode(&rule); err != nil {
		return defaultRule, err
	}

	t.mu.Lock()
	t.rules[serviceName] = rule
	t.rulesExpiry[serviceName] = time.Now().Add(t.cacheTTL)
	t.mu.Unlock()

	return rule, nil
}
