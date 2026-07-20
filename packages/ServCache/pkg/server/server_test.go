package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type mockCache struct {
	getFunc           func(key string) (interface{}, bool, error)
	setFunc           func(key string, val interface{}, ttl time.Duration) error
	deleteFunc        func(key string) error
	clearFunc         func() error
	deletePatternFunc func(pattern string) error
}

func (m *mockCache) Get(key string) (interface{}, bool, error) {
	if m.getFunc != nil {
		return m.getFunc(key)
	}
	return nil, false, nil
}

func (m *mockCache) Set(key string, value interface{}, ttl time.Duration) error {
	if m.setFunc != nil {
		return m.setFunc(key, value, ttl)
	}
	return nil
}

func (m *mockCache) Delete(key string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(key)
	}
	return nil
}

func (m *mockCache) Clear() error {
	if m.clearFunc != nil {
		return m.clearFunc()
	}
	return nil
}

func (m *mockCache) DeletePattern(pattern string) error {
	if m.deletePatternFunc != nil {
		return m.deletePatternFunc(pattern)
	}
	return nil
}

func TestNewServerEmptyPeers(t *testing.T) {
	s := NewServer(&mockCache{})
	if len(s.peers) != 0 {
		t.Errorf("expected no peers, got %d", len(s.peers))
	}
}

func TestNewServerPeersEnv(t *testing.T) {
	os.Setenv("SERV_CACHE_PEERS", "http://peer1,http://peer2")
	defer os.Unsetenv("SERV_CACHE_PEERS")
	s := NewServer(&mockCache{})
	if len(s.peers) != 2 || s.peers[0] != "http://peer1" || s.peers[1] != "http://peer2" {
		t.Errorf("incorrect peers parsed: %v", s.peers)
	}
}

func TestHandlerHealthz(t *testing.T) {
	s := NewServer(&mockCache{})
	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
}

func TestHandlerReadyz(t *testing.T) {
	s := NewServer(&mockCache{})
	req := httptest.NewRequest("GET", "/readyz", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
}

func TestHandlerVersion(t *testing.T) {
	s := NewServer(&mockCache{})
	req := httptest.NewRequest("GET", "/api/version", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
}

func TestHandlerHealth(t *testing.T) {
	s := NewServer(&mockCache{})
	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
}

func TestHandlerGossipInvalidateMethodNotAllowed(t *testing.T) {
	s := NewServer(&mockCache{})
	req := httptest.NewRequest("GET", "/api/cache/gossip-invalidate", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rr.Code)
	}
}

func TestHandlerCacheMethodNotAllowed(t *testing.T) {
	s := NewServer(&mockCache{})
	req := httptest.NewRequest("GET", "/api/cache", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rr.Code)
	}
}

func TestHandlerCacheKeyRequired(t *testing.T) {
	s := NewServer(&mockCache{})
	req := httptest.NewRequest("GET", "/api/cache/", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rr.Code)
	}
}

func TestHandlerInspect(t *testing.T) {
	s := NewServer(&mockCache{})
	req := httptest.NewRequest("GET", "/api/cache/inspect", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
}

func TestTrackHotKey(t *testing.T) {
	s := NewServer(&mockCache{})
	s.trackHotKey("hotkey")
	s.trackHotKey("hotkey")
	s.hotKeysMu.Lock()
	count := s.hotKeys["hotkey"]
	s.hotKeysMu.Unlock()
	if count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}
}

func TestInspectHandlerEmpty(t *testing.T) {
	s := NewServer(&mockCache{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/cache/inspect", nil)
	s.handleInspect(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"hot_keys"`) {
		t.Errorf("expected json containing hot_keys, got %s", rr.Body.String())
	}
}

func TestInspectHandlerWithData(t *testing.T) {
	s := NewServer(&mockCache{})
	s.trackHotKey("test_key")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/cache/inspect", nil)
	s.handleInspect(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"test_key"`) {
		t.Errorf("expected json containing test_key, got %s", rr.Body.String())
	}
}

func TestClearHandler(t *testing.T) {
	clearCalled := false
	mc := &mockCache{
		clearFunc: func() error {
			clearCalled = true
			return nil
		},
	}
	s := NewServer(mc)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/cache", nil)
	s.handleClear(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
	if !clearCalled {
		t.Error("expected Clear cache to be called")
	}
}

func TestDeleteHandlerMiss(t *testing.T) {
	deleteCalled := false
	mc := &mockCache{
		deleteFunc: func(key string) error {
			if key == "test_key" {
				deleteCalled = true
			}
			return nil
		},
	}
	s := NewServer(mc)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/cache/test_key", nil)
	s.handleDelete(rr, req, "test_key")
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
	if !deleteCalled {
		t.Error("expected Delete cache to be called")
	}
}

func TestGetHandlerMiss(t *testing.T) {
	mc := &mockCache{
		getFunc: func(key string) (interface{}, bool, error) {
			return nil, false, nil
		},
	}
	s := NewServer(mc)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/cache/absent", nil)
	s.handleGet(rr, req, "absent")
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", rr.Code)
	}
}

func TestServerMuxMetrics(t *testing.T) {
	s := NewServer(&mockCache{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "servcache_hits_total") {
		t.Errorf("expected metric 'servcache_hits_total' in metrics page")
	}
}

func TestGossipInvalidateKeyMissing(t *testing.T) {
	s := NewServer(&mockCache{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/cache/gossip-invalidate", nil)
	s.handleGossipInvalidate(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rr.Code)
	}
}
