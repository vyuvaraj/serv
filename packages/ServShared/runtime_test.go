package ServShared_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	ServShared "github.com/vyuvaraj/ServShared"
)

// --- Fake mesh resolver for unit tests -------------------------------------

type fakeMeshResolver struct {
	registerCalls atomic.Int32
	heartbeats    atomic.Int32
	registerErr   error
	heartbeatErr  error
}

func (f *fakeMeshResolver) Register(_ context.Context, _, _, _ string) error {
	f.registerCalls.Add(1)
	return f.registerErr
}

func (f *fakeMeshResolver) Heartbeat(_ context.Context, _, _ string) error {
	f.heartbeats.Add(1)
	return f.heartbeatErr
}

// ---------------------------------------------------------------------------

func TestDefaultRuntimeConfig_Defaults(t *testing.T) {
	cfg := ServShared.DefaultRuntimeConfig()

	if cfg.MeshAddr != "http://localhost:8089" {
		t.Errorf("expected default MeshAddr, got %q", cfg.MeshAddr)
	}
	if cfg.HeartbeatTTL != 5*time.Second {
		t.Errorf("expected 5s heartbeat TTL, got %v", cfg.HeartbeatTTL)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("expected 3 max retries, got %d", cfg.MaxRetries)
	}
	if cfg.HealthPath != "/healthz" {
		t.Errorf("expected /healthz health path, got %q", cfg.HealthPath)
	}
	if !cfg.EnableOtel {
		t.Error("expected OTel enabled by default")
	}
}

func TestRuntimeConfig_EnvOverride(t *testing.T) {
	t.Setenv("SERV_MESH_ADDR", "http://mesh.internal:9000")
	t.Setenv("SERV_HEARTBEAT_TTL", "30")
	t.Setenv("SERV_MAX_RETRIES", "5")
	t.Setenv("SERV_OTEL_ENABLED", "false")

	cfg := ServShared.DefaultRuntimeConfig()

	if cfg.MeshAddr != "http://mesh.internal:9000" {
		t.Errorf("expected overridden MeshAddr, got %q", cfg.MeshAddr)
	}
	if cfg.HeartbeatTTL != 30*time.Second {
		t.Errorf("expected 30s heartbeat, got %v", cfg.HeartbeatTTL)
	}
	if cfg.MaxRetries != 5 {
		t.Errorf("expected 5 retries, got %d", cfg.MaxRetries)
	}
	if cfg.EnableOtel {
		t.Error("expected OTel disabled via env")
	}
}

func TestFunctionalOptions(t *testing.T) {
	rt := ServShared.NewRuntime("svc-test",
		ServShared.WithMeshAddr("http://override:9999"),
		ServShared.WithSelfAddr("http://me:8080"),
		ServShared.WithHealthPath("/ready"),
		ServShared.WithHeartbeatTTL(15*time.Second),
		ServShared.WithMaxRetries(7),
		ServShared.WithRegion("us-east"),
		ServShared.WithOtel(false),
	)

	cfg := rt.Config
	if cfg.MeshAddr != "http://override:9999" {
		t.Errorf("MeshAddr option not applied, got %q", cfg.MeshAddr)
	}
	if cfg.SelfAddr != "http://me:8080" {
		t.Errorf("SelfAddr option not applied, got %q", cfg.SelfAddr)
	}
	if cfg.HealthPath != "/ready" {
		t.Errorf("HealthPath option not applied, got %q", cfg.HealthPath)
	}
	if cfg.HeartbeatTTL != 15*time.Second {
		t.Errorf("HeartbeatTTL option not applied, got %v", cfg.HeartbeatTTL)
	}
	if cfg.MaxRetries != 7 {
		t.Errorf("MaxRetries option not applied, got %d", cfg.MaxRetries)
	}
	if cfg.Region != "us-east" {
		t.Errorf("Region option not applied, got %q", cfg.Region)
	}
	if cfg.EnableOtel {
		t.Error("Otel option not applied: expected false")
	}
}

func TestServRuntime_Start_RegistersWithMesh(t *testing.T) {
	fake := &fakeMeshResolver{}

	rt := ServShared.NewRuntime("test-svc",
		ServShared.WithSelfAddr("http://localhost:8080"),
		ServShared.WithHeartbeatTTL(100*time.Millisecond),
		ServShared.WithOtel(false),
	)
	rt.SetResolver(fake)

	ctx := context.Background()
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}
	defer rt.Stop()

	if fake.registerCalls.Load() != 1 {
		t.Errorf("expected 1 Register call, got %d", fake.registerCalls.Load())
	}
}

func TestServRuntime_Heartbeat_Fired(t *testing.T) {
	fake := &fakeMeshResolver{}

	rt := ServShared.NewRuntime("hb-svc",
		ServShared.WithSelfAddr("http://localhost:8080"),
		ServShared.WithHeartbeatTTL(50*time.Millisecond),
		ServShared.WithOtel(false),
	)
	rt.SetResolver(fake)

	ctx := context.Background()
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}

	time.Sleep(180 * time.Millisecond) // allow ~3 heartbeat ticks
	rt.Stop()

	beats := fake.heartbeats.Load()
	if beats < 2 {
		t.Errorf("expected at least 2 heartbeats, got %d", beats)
	}
}

func TestServRuntime_Stop_CancelsHeartbeat(t *testing.T) {
	fake := &fakeMeshResolver{}

	rt := ServShared.NewRuntime("stop-svc",
		ServShared.WithSelfAddr("http://localhost:8080"),
		ServShared.WithHeartbeatTTL(30*time.Millisecond),
		ServShared.WithOtel(false),
	)
	rt.SetResolver(fake)

	ctx := context.Background()
	_ = rt.Start(ctx)
	time.Sleep(60 * time.Millisecond)
	rt.Stop()

	snapshot := fake.heartbeats.Load()
	time.Sleep(100 * time.Millisecond) // no more ticks should occur

	if final := fake.heartbeats.Load(); final != snapshot {
		t.Errorf("heartbeat loop continued after Stop(): %d -> %d", snapshot, final)
	}
}

func TestServRuntime_NoResolver_StartsCleanly(t *testing.T) {
	rt := ServShared.NewRuntime("no-mesh-svc",
		ServShared.WithOtel(false),
	)
	// no resolver set
	ctx := context.Background()
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start() without resolver should not error, got: %v", err)
	}
	rt.Stop()
}

func TestHTTPMeshResolver_Register(t *testing.T) {
	var received map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received) //nolint:errcheck
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	resolver := ServShared.NewHTTPMeshResolver(srv.URL)
	err := resolver.Register(context.Background(), "my-svc", "http://localhost:9090", "http://localhost:9090/healthz")
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if received["service"] != "my-svc" {
		t.Errorf("expected service=my-svc in payload, got %q", received["service"])
	}
	if received["address"] != "http://localhost:9090" {
		t.Errorf("unexpected address: %q", received["address"])
	}
}

func TestHTTPMeshResolver_Heartbeat(t *testing.T) {
	var received map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received) //nolint:errcheck
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	resolver := ServShared.NewHTTPMeshResolver(srv.URL)
	err := resolver.Heartbeat(context.Background(), "my-svc", "http://localhost:9090")
	if err != nil {
		t.Fatalf("Heartbeat returned error: %v", err)
	}
	if received["service"] != "my-svc" {
		t.Errorf("expected service=my-svc in heartbeat payload, got %q", received["service"])
	}
}

func TestHTTPMeshResolver_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	resolver := ServShared.NewHTTPMeshResolver(srv.URL)
	err := resolver.Heartbeat(context.Background(), "ghost-svc", "http://localhost:9999")
	if err == nil {
		t.Fatal("expected error for 404 response, got nil")
	}
}
