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
	"os"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"servmesh/pkg/pb"
	"servmesh/pkg/registry"
	"servmesh/pkg/resilience"
	"github.com/vyuvaraj/ServShared"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
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
	latencies   map[string]time.Duration
	errorRates  map[string]float64
	
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
		latencies:   make(map[string]time.Duration),
		errorRates:  make(map[string]float64),
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

		var resp *http.Response
		startTime := time.Now()
		if os.Getenv("SERV_MESH_GRPC") == "true" {
			grpcHost := getGRPCHost(target)
			conn, grpcErr := grpc.Dial(grpcHost, grpc.WithInsecure(), grpc.WithBlock(), grpc.WithTimeout(1*time.Second))
			if grpcErr == nil {
				defer conn.Close()
				client := pb.NewMeshServiceClient(conn)
				headersMap := make(map[string]string)
				for k := range clonedReq.Header {
					headersMap[k] = clonedReq.Header.Get(k)
				}
				grpcReq := &pb.MeshRequest{
					Method:  clonedReq.Method,
					Path:    clonedReq.URL.Path,
					Headers: headersMap,
					Body:    bodyBytes,
				}
				grpcResp, callErr := client.Forward(ctx, grpcReq)
				if callErr == nil {
					respHeaders := make(http.Header)
					for k, v := range grpcResp.Headers {
						respHeaders.Set(k, v)
					}
					resp = &http.Response{
						StatusCode: int(grpcResp.StatusCode),
						Header:     respHeaders,
						Body:       io.NopCloser(bytes.NewReader(grpcResp.Body)),
						Request:    clonedReq,
					}
				} else {
					err = callErr
				}
			} else {
				err = grpcErr
			}
		} else {
			resp, err = t.base.RoundTrip(clonedReq)
		}

		elapsed := time.Since(startTime)

		t.mu.Lock()
		oldLatency := t.latencies[target]
		if oldLatency == 0 {
			t.latencies[target] = elapsed
		} else {
			t.latencies[target] = time.Duration(0.9*float64(oldLatency) + 0.1*float64(elapsed))
		}
		oldErrRate := t.errorRates[target]
		isErr := 0.0
		if err != nil || (resp != nil && resp.StatusCode >= 500) {
			isErr = 1.0
		}
		t.errorRates[target] = 0.9*oldErrRate + 0.1*isErr
		t.mu.Unlock()

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

	// Calculate average latency among available targets
	var totalLat time.Duration
	var latCount int
	for _, inst := range available {
		if lat, ok := t.latencies[inst.Address]; ok && lat > 0 {
			totalLat += lat
			latCount++
		}
	}
	var avgLat time.Duration
	if latCount > 0 {
		avgLat = totalLat / time.Duration(latCount)
	}

	// Check if any target is degraded
	degraded := false
	if avgLat > 0 {
		for _, inst := range available {
			if errRate, ok := t.errorRates[inst.Address]; ok && errRate > 0.05 {
				degraded = true
				break
			}
			if lat, ok := t.latencies[inst.Address]; ok && lat > 0 {
				ratio := float64(lat) / float64(avgLat)
				if ratio > 1.3 {
					degraded = true
					break
				}
			}
		}
	} else {
		for _, inst := range available {
			if errRate, ok := t.errorRates[inst.Address]; ok && errRate > 0.05 {
				degraded = true
				break
			}
		}
	}

	// Check if weights are all default
	allDefault := true
	for _, inst := range available {
		if inst.Weight > 0 {
			allDefault = false
			break
		}
	}

	if !degraded && allDefault {
		idx := t.rrIndex[serviceName]
		selected := available[idx%len(available)].Address
		t.rrIndex[serviceName] = (idx + 1) % len(available)
		return selected
	}

	// Calculate adjusted weights based on OTel health feedback
	adjustedWeights := make([]int, len(available))
	totalWeight := 0
	for idx, inst := range available {
		w := inst.Weight
		if w <= 0 {
			w = 100
		}
		
		multiplier := 1.0
		if errRate, ok := t.errorRates[inst.Address]; ok {
			multiplier *= (1.0 - errRate)
		}
		if avgLat > 0 {
			if lat, ok := t.latencies[inst.Address]; ok && lat > 0 {
				ratio := float64(lat) / float64(avgLat)
				multiplier *= (1.0 / (1.0 + (ratio-1.0)*0.5))
			}
		}
		if multiplier < 0.05 {
			multiplier = 0.05 // Floor at 5%
		}

		adjW := int(float64(w) * multiplier)
		if adjW < 1 {
			adjW = 1
		}
		adjustedWeights[idx] = adjW
		totalWeight += adjW
	}

	// Perform weighted selection
	val := mrand.Intn(totalWeight)
	current := 0
	for idx, inst := range available {
		current += adjustedWeights[idx]
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

func getGRPCHost(httpAddr string) string {
	u, err := url.Parse(httpAddr)
	if err != nil {
		return httpAddr
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		return host + ":9000"
	}
	var portInt int
	fmt.Sscanf(port, "%d", &portInt)
	return fmt.Sprintf("%s:%d", host, portInt+1000)
}

func StartGRPCProxy(grpcAddr string, httpHandler http.Handler) (*grpc.Server, error) {
	return StartGRPCProxyWithRegistry(grpcAddr, httpHandler, nil, "")
}

func StartGRPCProxyWithRegistry(grpcAddr string, httpHandler http.Handler, reg *registry.Registry, targetService string) (*grpc.Server, error) {
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return nil, err
	}
	s := grpc.NewServer()
	pb.RegisterMeshServiceServer(s, &grpcProxyServer{
		handler:       httpHandler,
		registry:      reg,
		targetService: targetService,
	})
	go s.Serve(lis)
	return s, nil
}

type grpcProxyServer struct {
	handler       http.Handler
	registry      *registry.Registry
	targetService string
}

func (p *grpcProxyServer) Forward(ctx context.Context, in *pb.MeshRequest) (*pb.MeshResponse, error) {
	var clientIdentity string
	if pr, ok := peer.FromContext(ctx); ok && pr.AuthInfo != nil {
		if tlsInfo, ok := pr.AuthInfo.(credentials.TLSInfo); ok {
			if len(tlsInfo.State.PeerCertificates) > 0 {
				clientIdentity = tlsInfo.State.PeerCertificates[0].Subject.CommonName
			}
		}
	}
	if clientIdentity == "" {
		clientIdentity = in.Headers["X-Mesh-Source"]
	}

	if p.registry != nil && clientIdentity != "" {
		if !p.registry.ValidateNetworkPolicy(clientIdentity, p.targetService, in.Path) {
			return &pb.MeshResponse{
				StatusCode: http.StatusForbidden,
				Body:       []byte("mTLS Network Policy Blocked: Access Denied"),
			}, nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, in.Method, in.Path, bytes.NewReader(in.Body))
	if err != nil {
		return nil, err
	}
	for k, v := range in.Headers {
		req.Header.Set(k, v)
	}

	w := httptest.NewRecorder()
	p.handler.ServeHTTP(w, req)

	respHeaders := make(map[string]string)
	for k := range w.Header() {
		respHeaders[k] = w.Header().Get(k)
	}

	return &pb.MeshResponse{
		StatusCode: int32(w.Code),
		Headers:    respHeaders,
		Body:       w.Body.Bytes(),
	}, nil
}

func (t *MeshTransport) SelectTargetForTest(serviceName string, targets []registry.Instance) string {
	return t.selectTarget(serviceName, targets)
}

func (t *MeshTransport) UpdateTargetMetricsForTest(target string, elapsed time.Duration, isErr bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.latencies[target] = elapsed
	errVal := 0.0
	if isErr {
		errVal = 1.0
	}
	t.errorRates[target] = errVal
}
