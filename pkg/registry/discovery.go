package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/hashicorp/consul/api"
	"go.etcd.io/etcd/client/v3"
)

type DiscoveryBackend interface {
	Register(ctx context.Context, service string, addr string, version string, region string) error
	Deregister(ctx context.Context, service string, addr string) error
	Resolve(ctx context.Context, service string) ([]Instance, error)
}

// ── 1. Consul Discovery Backend ───────────────────────────────────────────────

type ConsulDiscoveryBackend struct {
	client *api.Client
}

func NewConsulDiscoveryBackend(address string) (*ConsulDiscoveryBackend, error) {
	cfg := api.DefaultConfig()
	if address != "" {
		cfg.Address = address
	}
	c, err := api.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &ConsulDiscoveryBackend{client: c}, nil
}

func (c *ConsulDiscoveryBackend) Register(ctx context.Context, service string, addr string, version string, region string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	var port int
	_, sscanfErr := fmt.Sscanf(portStr, "%d", &port)
	if sscanfErr != nil {
		return fmt.Errorf("invalid port in address %s: %w", addr, sscanfErr)
	}

	meta := make(map[string]string)
	if version != "" {
		meta["version"] = version
	}
	if region != "" {
		meta["region"] = region
	}

	reg := &api.AgentServiceRegistration{
		ID:      fmt.Sprintf("%s-%s", service, addr),
		Name:    service,
		Address: host,
		Port:    port,
		Meta:    meta,
	}

	return c.client.Agent().ServiceRegister(reg)
}

func (c *ConsulDiscoveryBackend) Deregister(ctx context.Context, service string, addr string) error {
	id := fmt.Sprintf("%s-%s", service, addr)
	return c.client.Agent().ServiceDeregister(id)
}

func (c *ConsulDiscoveryBackend) Resolve(ctx context.Context, service string) ([]Instance, error) {
	services, _, err := c.client.Catalog().Service(service, "", nil)
	if err != nil {
		return nil, err
	}

	var instances []Instance
	for _, s := range services {
		addr := net.JoinHostPort(s.ServiceAddress, fmt.Sprintf("%d", s.ServicePort))
		instances = append(instances, Instance{
			Service:   service,
			Address:   addr,
			HealthURL: fmt.Sprintf("http://%s/health", addr),
			LastSeen:  time.Now(),
			Version:   s.ServiceMeta["version"],
			Region:    s.ServiceMeta["region"],
			Weight:    1,
		})
	}
	return instances, nil
}

// ── 2. etcd Discovery Backend ────────────────────────────────────────────────

type EtcdDiscoveryBackend struct {
	client *clientv3.Client
}

func NewEtcdDiscoveryBackend(endpoints []string) (*EtcdDiscoveryBackend, error) {
	c, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &EtcdDiscoveryBackend{client: c}, nil
}

func (e *EtcdDiscoveryBackend) Register(ctx context.Context, service string, addr string, version string, region string) error {
	key := fmt.Sprintf("/services/%s/%s", service, addr)
	inst := Instance{
		Service:   service,
		Address:   addr,
		HealthURL: fmt.Sprintf("http://%s/health", addr),
		LastSeen:  time.Now(),
		Version:   version,
		Region:    region,
		Weight:    1,
	}
	val, err := jsonMarshal(inst)
	if err != nil {
		return err
	}
	_, err = e.client.Put(ctx, key, val)
	return err
}

func (e *EtcdDiscoveryBackend) Deregister(ctx context.Context, service string, addr string) error {
	key := fmt.Sprintf("/services/%s/%s", service, addr)
	_, err := e.client.Delete(ctx, key)
	return err
}

func (e *EtcdDiscoveryBackend) Resolve(ctx context.Context, service string) ([]Instance, error) {
	prefix := fmt.Sprintf("/services/%s/", service)
	resp, err := e.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}

	var instances []Instance
	for _, kv := range resp.Kvs {
		var inst Instance
		if err := jsonUnmarshal(kv.Value, &inst); err == nil {
			instances = append(instances, inst)
		}
	}
	return instances, nil
}

// ── 3. DNS-SD Discovery Backend ──────────────────────────────────────────────

type DNSSDDiscoveryBackend struct{}

func NewDNSSDDiscoveryBackend() *DNSSDDiscoveryBackend {
	return &DNSSDDiscoveryBackend{}
}

func (d *DNSSDDiscoveryBackend) Register(ctx context.Context, service string, addr string, version string, region string) error {
	return nil
}

func (d *DNSSDDiscoveryBackend) Deregister(ctx context.Context, service string, addr string) error {
	return nil
}

func (d *DNSSDDiscoveryBackend) Resolve(ctx context.Context, service string) ([]Instance, error) {
	_, addrs, err := net.LookupSRV(service, "tcp", "local")
	if err != nil {
		_, addrs, err = net.LookupSRV("", "", service)
		if err != nil {
			return nil, err
		}
	}

	var instances []Instance
	for _, srv := range addrs {
		target := strings.TrimSuffix(srv.Target, ".")
		addr := net.JoinHostPort(target, fmt.Sprintf("%d", srv.Port))
		instances = append(instances, Instance{
			Service:   service,
			Address:   addr,
			HealthURL: fmt.Sprintf("http://%s/health", addr),
			LastSeen:  time.Now(),
			Weight:    int(srv.Weight),
		})
	}
	return instances, nil
}

func jsonMarshal(v interface{}) (string, error) {
	inst, ok := v.(Instance)
	if !ok {
		return "", fmt.Errorf("invalid type")
	}
	b, err := json.Marshal(inst)
	return string(b), err
}

func jsonUnmarshal(data []byte, v *Instance) error {
	return json.Unmarshal(data, v)
}
