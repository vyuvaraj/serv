package registry

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Instance struct {
	Service   string    `json:"service"`
	Address   string    `json:"address"`
	HealthURL string    `json:"health_url"`
	LastSeen  time.Time `json:"last_seen"`
}

type Registry struct {
	mu        sync.RWMutex
	instances map[string][]Instance // key: service name
	ttl       time.Duration
}

func NewRegistry(ttl time.Duration) *Registry {
	r := &Registry{
		instances: make(map[string][]Instance),
		ttl:       ttl,
	}
	go r.startEvictionLoop(2 * time.Second)
	return r
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

	mux.HandleFunc("/health", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
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
		instances := r.Resolve(serviceName)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(instances)
	})

	return mux
}
