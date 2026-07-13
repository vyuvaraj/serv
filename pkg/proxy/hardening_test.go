package proxy

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRateLimiterBurstAccuracyHardening(t *testing.T) {
	// Setup a GatewayHandler with a limit of 100 requests per minute
	routes := []Route{
		{
			Prefix:       "/limited",
			Target:       "http://localhost:8080",
			RateLimitRPM: 100,
		},
	}
	h := NewGatewayHandler(routes, nil, "")

	var allowedCount int64
	var blockedCount int64
	var wg sync.WaitGroup

	// Fire 150 concurrent requests simultaneously
	for i := 0; i < 150; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if h.isRateLimited("127.0.0.1", "/limited", 100) {
				atomic.AddInt64(&blockedCount, 1)
			} else {
				atomic.AddInt64(&allowedCount, 1)
			}
		}()
	}
	wg.Wait()

	if allowedCount != 100 {
		t.Errorf("expected exactly 100 allowed requests, got %d", allowedCount)
	}
	if blockedCount != 50 {
		t.Errorf("expected exactly 50 blocked requests, got %d", blockedCount)
	}
}

func TestConfigReloadZeroDroppedRequests(t *testing.T) {
	backend1Called := int32(0)
	backend2Called := int32(0)

	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&backend1Called, 1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backend1"))
	}))
	defer backend1.Close()

	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&backend2Called, 1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backend2"))
	}))
	defer backend2.Close()

	// Initial configuration routing to backend 1
	routes := []Route{
		{Prefix: "/api", Target: backend1.URL},
	}
	handler := NewGatewayHandler(routes, nil, "")
	gwServer := httptest.NewServer(handler)
	defer gwServer.Close()

	client := gwServer.Client()
	var wg sync.WaitGroup
	var droppedCount int64

	// Concurrently send requests while reloading the config
	stopChan := make(chan struct{})

	// 1. Start sending requests in parallel
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopChan:
					return
				default:
					resp, err := client.Get(gwServer.URL + "/api/test")
					if err != nil {
						atomic.AddInt64(&droppedCount, 1)
						continue
					}
					resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						atomic.AddInt64(&droppedCount, 1)
					}
				}
			}
		}()
	}

	// 2. Perform route configuration reloads mid-flight
	time.Sleep(50 * time.Millisecond)
	newRoutes := []Route{
		{Prefix: "/api", Target: backend2.URL},
	}
	handler.UpdateRoutes(newRoutes)

	time.Sleep(50 * time.Millisecond)
	close(stopChan)
	wg.Wait()

	if droppedCount > 0 {
		t.Errorf("expected 0 dropped requests during hot-reload, got %d failures", droppedCount)
	}

	b1 := atomic.LoadInt32(&backend1Called)
	b2 := atomic.LoadInt32(&backend2Called)

	if b1 == 0 || b2 == 0 {
		t.Errorf("expected both backends to be invoked during reload lifecycle: backend1=%d, backend2=%d", b1, b2)
	}
}
