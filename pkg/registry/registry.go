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
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/vyuvaraj/ServShared"
)

type Instance struct {
	Service   string    `json:"service"`
	Address   string    `json:"address"`
	HealthURL string    `json:"health_url"`
	LastSeen  time.Time `json:"last_seen"`
	Version   string    `json:"version,omitempty"`
	Weight    int       `json:"weight,omitempty"`
}

type Registry struct {
	mu        sync.RWMutex
	instances map[string][]Instance // key: service name
	ttl       time.Duration

	caCert    *x509.Certificate
	caPrivKey *ecdsa.PrivateKey
}

func NewRegistry(ttl time.Duration) *Registry {
	r := &Registry{
		instances: make(map[string][]Instance),
		ttl:       ttl,
	}
	r.generateRootCA()
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

	jwtSecret := os.Getenv("SERV_JWT_SECRET")
	var validator *ServShared.AuthValidator
	if jwtSecret != "" {
		validator = ServShared.NewAuthValidator(jwtSecret, "", "")
	}

	checkAuth := func(w http.ResponseWriter, req *http.Request) bool {
		if validator == nil {
			return true
		}
		authHeader := req.Header.Get("Authorization")
		token, err := ServShared.ExtractTokenFromHeader(authHeader)
		if err != nil {
			http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
			return false
		}
		_, err = validator.ValidateToken(token)
		if err != nil {
			http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
			return false
		}
		return true
	}

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
		if !checkAuth(w, req) {
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
		if !checkAuth(w, req) {
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
		if !checkAuth(w, req) {
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
		instances := r.Resolve(serviceName)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(instances)
	})

	return mux
}
