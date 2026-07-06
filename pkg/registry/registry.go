package registry

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vyuvaraj/ServShared"
	"servmesh/pkg/lock"
)

type Instance struct {
	Service   string    `json:"service"`
	Address   string    `json:"address"`
	HealthURL string    `json:"health_url"`
	LastSeen  time.Time `json:"last_seen"`
	Version   string    `json:"version,omitempty"`
	Weight    int       `json:"weight,omitempty"`
	Region    string    `json:"region,omitempty"`
}

type RoutingRule struct {
	Service    string  `json:"service"`
	MaxRetries int     `json:"max_retries"`
	TimeoutMs  int     `json:"timeout_ms"`
	BackoffMs  int     `json:"backoff_ms"`
	FaultDelayMs     int     `json:"fault_delay_ms,omitempty"`
	FaultDelayRatio  float64 `json:"fault_delay_ratio,omitempty"`
	FaultErrorStatus int     `json:"fault_error_status,omitempty"`
	FaultErrorRatio  float64 `json:"fault_error_ratio,omitempty"`
}

type NetworkPolicy struct {
	SourceService string   `json:"source_service"`
	TargetService string   `json:"target_service"`
	AllowedPaths  []string `json:"allowed_paths"`
}

type Registry struct {
	mu        sync.RWMutex
	instances map[string][]Instance // key: service name
	rules     map[string]RoutingRule
	policies  map[string][]NetworkPolicy // key: target service name
	ttl       time.Duration

	caCert        *x509.Certificate
	caPrivKey     *ecdsa.PrivateKey
	multicastConn *net.UDPConn

	// locks is the distributed lock store embedded in the registry.
	locks *lock.Store
}

func NewRegistry(ttl time.Duration) *Registry {
	r := &Registry{
		instances: make(map[string][]Instance),
		rules:     make(map[string]RoutingRule),
		policies:  make(map[string][]NetworkPolicy),
		ttl:       ttl,
		locks:     lock.NewStore(ttl),
	}
	r.generateRootCA()
	r.startMulticastListener()
	go r.startEvictionLoop(2 * time.Second)
	return r
}

func (r *Registry) generateRootCA() {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return
	}
	r.caPrivKey = priv

	serialNumber, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "ServMesh Root CA",
			Organization: []string{"Servverse"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return
	}

	caCert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		return
	}
	r.caCert = caCert
}

func (r *Registry) Register(inst Instance) {
	r.mu.Lock()
	defer r.mu.Unlock()

	inst.LastSeen = time.Now()
	service := strings.ToLower(inst.Service)

	list := r.instances[service]
	found := false
	for i, existing := range list {
		if existing.Address == inst.Address {
			list[i] = inst
			found = true
			break
		}
	}
	if !found {
		list = append(list, inst)
	}
	r.instances[service] = list
}

func (r *Registry) Heartbeat(service, address string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	service = strings.ToLower(service)
	list, ok := r.instances[service]
	if !ok {
		return false
	}

	for i, existing := range list {
		if existing.Address == address {
			list[i].LastSeen = time.Now()
			r.instances[service] = list
			return true
		}
	}
	return false
}

func (r *Registry) Resolve(service string) []Instance {
	r.mu.RLock()
	defer r.mu.RUnlock()

	service = strings.ToLower(service)
	list := r.instances[service]
	healthy := make([]Instance, len(list))
	copy(healthy, list)
	return healthy
}

func (r *Registry) ResolveRegion(service, region string) []Instance {
	r.mu.RLock()
	defer r.mu.RUnlock()

	service = strings.ToLower(service)
	list := r.instances[service]

	var regional []Instance
	for _, inst := range list {
		if region != "" && strings.EqualFold(inst.Region, region) {
			regional = append(regional, inst)
		}
	}

	if len(regional) > 0 {
		return regional
	}

	healthy := make([]Instance, len(list))
	copy(healthy, list)
	return healthy
}

func (r *Registry) Evict() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	for service, list := range r.instances {
		var active []Instance
		for _, inst := range list {
			if now.Sub(inst.LastSeen) <= r.ttl {
				active = append(active, inst)
			}
		}
		if len(active) == 0 {
			delete(r.instances, service)
		} else {
			r.instances[service] = active
		}
	}
}

func (r *Registry) startEvictionLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	for range ticker.C {
		r.Evict()
	}
}

func (r *Registry) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", ServShared.HealthzHandler)
	mux.HandleFunc("/readyz", ServShared.ReadyzHandler)
	mux.HandleFunc("/api/version", ServShared.VersionHandler("servmesh", "1.0.0"))
	mux.HandleFunc("/health", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	mux.HandleFunc("/api/ca", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.caCert == nil {
			http.Error(w, "CA not initialized", http.StatusInternalServerError)
			return
		}
		caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: r.caCert.Raw})
		w.Header().Set("Content-Type", "text/plain")
		w.Write(caPEM)
	})

	mux.HandleFunc("/api/csr", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.caCert == nil || r.caPrivKey == nil {
			http.Error(w, "CA not initialized", http.StatusInternalServerError)
			return
		}

		var body struct {
			Service string `json:"service"`
			CSR     string `json:"csr"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		block, _ := pem.Decode([]byte(body.CSR))
		if block == nil || block.Type != "CERTIFICATE REQUEST" {
			http.Error(w, "invalid CSR PEM", http.StatusBadRequest)
			return
		}

		csr, err := x509.ParseCertificateRequest(block.Bytes)
		if err != nil {
			http.Error(w, "failed to parse CSR: "+err.Error(), http.StatusBadRequest)
			return
		}

		if err := csr.CheckSignature(); err != nil {
			http.Error(w, "invalid CSR signature", http.StatusBadRequest)
			return
		}

		serialNumber, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		template := &x509.Certificate{
			SerialNumber: serialNumber,
			Subject: pkix.Name{
				CommonName:   body.Service + ".servverse",
				Organization: []string{"Servverse"},
			},
			DNSNames:    []string{body.Service, body.Service + ".servverse", "localhost"},
			NotBefore:   time.Now().Add(-1 * time.Hour),
			NotAfter:    time.Now().Add(30 * 24 * time.Hour),
			KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		}

		certBytes, err := x509.CreateCertificate(rand.Reader, template, r.caCert, csr.PublicKey, r.caPrivKey)
		if err != nil {
			http.Error(w, "failed to sign certificate: "+err.Error(), http.StatusInternalServerError)
			return
		}

		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes})
		caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: r.caCert.Raw})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"certificate": string(certPEM),
			"ca":          string(caPEM),
		})
	})

	mux.HandleFunc("/api/register", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var inst Instance
		if err := json.NewDecoder(req.Body).Decode(&inst); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if inst.Service == "" || inst.Address == "" {
			http.Error(w, "Service and Address are required", http.StatusBadRequest)
			return
		}
		r.Register(inst)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`))
	})

	mux.HandleFunc("/api/heartbeat", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var inst struct {
			Service string `json:"service"`
			Address string `json:"address"`
		}
		if err := json.NewDecoder(req.Body).Decode(&inst); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.Heartbeat(inst.Service, inst.Address) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success"}`))
		} else {
			http.Error(w, "Instance not found", http.StatusNotFound)
		}
	})

	mux.HandleFunc("/api/resolve/", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
		if len(parts) < 3 {
			http.Error(w, "Service name required", http.StatusBadRequest)
			return
		}
		serviceName := parts[2]
		region := req.URL.Query().Get("region")
		var instances []Instance
		if region != "" {
			instances = r.ResolveRegion(serviceName, region)
		} else {
			instances = r.Resolve(serviceName)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(instances)
	})

	mux.HandleFunc("/api/rules", func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()

		switch req.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(r.rules)
			return
		case http.MethodPost:
			var rule RoutingRule
			if err := json.NewDecoder(req.Body).Decode(&rule); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if rule.Service == "" {
				http.Error(w, "Service name required", http.StatusBadRequest)
				return
			}
			r.rules[strings.ToLower(rule.Service)] = rule
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success"}`))
			return
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/rules/", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
		if len(parts) < 3 {
			http.Error(w, "Service name required", http.StatusBadRequest)
			return
		}
		serviceName := strings.ToLower(parts[2])

		r.mu.RLock()
		rule, ok := r.rules[serviceName]
		r.mu.RUnlock()

		if !ok {
			// Return default fallback rule
			rule = RoutingRule{
				Service:    serviceName,
				MaxRetries: 3,
				TimeoutMs:  2000,
				BackoffMs:  50,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rule)
	})

	mux.HandleFunc("/api/policies", func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()

		switch req.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(r.policies)
			return
		case http.MethodPost:
			var policy NetworkPolicy
			if err := json.NewDecoder(req.Body).Decode(&policy); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if policy.TargetService == "" || policy.SourceService == "" {
				http.Error(w, "Source and Target services required", http.StatusBadRequest)
				return
			}
			target := strings.ToLower(policy.TargetService)
			r.policies[target] = append(r.policies[target], policy)
			w.WriteHeader(http.StatusCreated)
			return
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	// ── Distributed Lock Manager ─────────────────────────────────────────────

	// POST /api/lock/acquire
	// Body: {"key":"<key>","owner":"<caller>","ttl_ms":<int>}
	// Response 200: {"acquired":true,"lock":{...}} or {"acquired":false,"held_by":"<owner>"}
	mux.HandleFunc("/api/lock/acquire", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Key   string `json:"key"`
			Owner string `json:"owner"`
			TTLMs int64  `json:"ttl_ms"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Key == "" || body.Owner == "" {
			http.Error(w, "key and owner are required", http.StatusBadRequest)
			return
		}
		ttl := time.Duration(body.TTLMs) * time.Millisecond
		result := r.locks.Acquire(body.Key, body.Owner, ttl)
		w.Header().Set("Content-Type", "application/json")
		if result.Acquired {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusConflict)
		}
		json.NewEncoder(w).Encode(result)
	})

	// POST /api/lock/release
	// Body: {"key":"<key>","owner":"<caller>"}
	// Response 200: {"released":true} or 409 {"released":false}
	mux.HandleFunc("/api/lock/release", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Key   string `json:"key"`
			Owner string `json:"owner"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		released := r.locks.Release(body.Key, body.Owner)
		w.Header().Set("Content-Type", "application/json")
		if released {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusConflict)
		}
		json.NewEncoder(w).Encode(map[string]bool{"released": released})
	})

	// POST /api/lock/extend
	// Body: {"key":"<key>","owner":"<caller>","ttl_ms":<int>}
	// Response 200: updated lock entry, 409 if not held by owner
	mux.HandleFunc("/api/lock/extend", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Key   string `json:"key"`
			Owner string `json:"owner"`
			TTLMs int64  `json:"ttl_ms"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ttl := time.Duration(body.TTLMs) * time.Millisecond
		entry, ok := r.locks.Extend(body.Key, body.Owner, ttl)
		w.Header().Set("Content-Type", "application/json")
		if ok {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(entry)
		} else {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "lock not held by owner or expired"})
		}
	})

	// GET /api/lock/status?key=<key>
	// Response 200: lock entry, 404 if not held
	mux.HandleFunc("/api/lock/status", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		key := req.URL.Query().Get("key")
		if key == "" {
			http.Error(w, "key query parameter required", http.StatusBadRequest)
			return
		}
		entry, ok := r.locks.Status(key)
		w.Header().Set("Content-Type", "application/json")
		if ok {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(entry)
		} else {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "lock not held"})
		}
	})

	// GET /api/lock/list
	// Response 200: array of all currently held locks
	mux.HandleFunc("/api/lock/list", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		entries := r.locks.List()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(entries)
	})

	return ServShared.AuthMiddleware(mux)
}

func (r *Registry) startMulticastListener() {
	addr, err := net.ResolveUDPAddr("udp4", "127.0.0.1:9999")
	var conn *net.UDPConn
	if err == nil {
		conn, _ = net.ListenUDP("udp4", addr)
	}

	if conn == nil {
		maddr, err := net.ResolveUDPAddr("udp4", "224.0.1.251:9999")
		if err == nil {
			conn, _ = net.ListenMulticastUDP("udp4", nil, maddr)
		}
	}

	if conn == nil {
		return
	}
	r.multicastConn = conn
	go func() {
		buf := make([]byte, 1024)
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			var packet struct {
				Type      string `json:"type"`
				Service   string `json:"service"`
				Address   string `json:"address"`
				HealthURL string `json:"health_url"`
			}
			if err := json.Unmarshal(buf[:n], &packet); err == nil {
				if packet.Type == "announce" && packet.Service != "" {
					r.Register(Instance{
						Service:   packet.Service,
						Address:   packet.Address,
						HealthURL: packet.HealthURL,
					})
				} else if packet.Type == "query" {
					r.mu.RLock()
					var instances []Instance
					for _, list := range r.instances {
						instances = append(instances, list...)
					}
					r.mu.RUnlock()

					for _, inst := range instances {
						respPacket := struct {
							Type      string `json:"type"`
							Service   string `json:"service"`
							Address   string `json:"address"`
							HealthURL string `json:"health_url"`
						}{
							Type:      "announce",
							Service:   inst.Service,
							Address:   inst.Address,
							HealthURL: inst.HealthURL,
						}
						respBytes, _ := json.Marshal(respPacket)
						_, _ = conn.WriteToUDP(respBytes, addr)
					}
				}
			}
		}
	}()
}

func (r *Registry) BroadcastQuery() {
	if r.multicastConn == nil {
		return
	}
	addr, err := net.ResolveUDPAddr("udp4", "127.0.0.1:9999")
	if err != nil {
		return
	}
	query := map[string]string{"type": "query"}
	data, _ := json.Marshal(query)
	_, _ = r.multicastConn.WriteToUDP(data, addr)
}

func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.multicastConn != nil {
		r.multicastConn.Close()
		r.multicastConn = nil
	}
	if r.locks != nil {
		r.locks.Close()
	}
}

func (r *Registry) ValidateNetworkPolicy(source, target, path string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	target = strings.ToLower(target)
	rules, exists := r.policies[target]
	if !exists || len(rules) == 0 {
		return true // Default allow if no policies configured for target
	}

	source = strings.ToLower(source)
	for _, p := range rules {
		if strings.ToLower(p.SourceService) == source {
			if len(p.AllowedPaths) == 0 {
				return true
			}
			for _, allowed := range p.AllowedPaths {
				if allowed == "*" || strings.HasPrefix(path, allowed) {
					return true
				}
			}
		}
	}
	return false
}
