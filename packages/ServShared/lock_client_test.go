package ServShared_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ServShared "github.com/vyuvaraj/serv/packages/ServShared"
)

// mockLockServer sets up an httptest.Server that implements the ServMesh
// /api/lock/* endpoints in memory using a trivial map — independent of the
// real lock.Store so this test file lives entirely within ServShared.
func mockLockServer(t *testing.T) *httptest.Server {
	t.Helper()

	type lockEntry struct {
		Key        string    `json:"key"`
		Owner      string    `json:"owner"`
		ExpiresAt  time.Time `json:"expires_at"`
		AcquiredAt time.Time `json:"acquired_at"`
	}

	store := map[string]*lockEntry{}

	mux := http.NewServeMux()

	mux.HandleFunc("/api/lock/acquire", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key   string `json:"key"`
			Owner string `json:"owner"`
			TTLMs int64  `json:"ttl_ms"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		ttl := time.Duration(body.TTLMs) * time.Millisecond
		if ttl <= 0 {
			ttl = 5 * time.Second
		}

		type result struct {
			Acquired bool        `json:"acquired"`
			Lock     *lockEntry  `json:"lock,omitempty"`
			HeldBy   string      `json:"held_by,omitempty"`
		}

		if existing, ok := store[body.Key]; ok && time.Now().Before(existing.ExpiresAt) {
			if existing.Owner == body.Owner {
				existing.ExpiresAt = time.Now().Add(ttl)
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(result{Acquired: true, Lock: existing})
				return
			}
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(result{Acquired: false, HeldBy: existing.Owner})
			return
		}

		e := &lockEntry{Key: body.Key, Owner: body.Owner, AcquiredAt: time.Now(), ExpiresAt: time.Now().Add(ttl)}
		store[body.Key] = e
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(result{Acquired: true, Lock: e})
	})

	mux.HandleFunc("/api/lock/release", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key   string `json:"key"`
			Owner string `json:"owner"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		existing, ok := store[body.Key]
		released := ok && existing.Owner == body.Owner
		if released {
			delete(store, body.Key)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]bool{"released": released})
	})

	mux.HandleFunc("/api/lock/extend", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key   string `json:"key"`
			Owner string `json:"owner"`
			TTLMs int64  `json:"ttl_ms"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		existing, ok := store[body.Key]
		if !ok || existing.Owner != body.Owner {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "not held"})
			return
		}
		ttl := time.Duration(body.TTLMs) * time.Millisecond
		if ttl <= 0 {
			ttl = 5 * time.Second
		}
		existing.ExpiresAt = time.Now().Add(ttl)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(existing)
	})

	mux.HandleFunc("/api/lock/status", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		existing, ok := store[key]
		if !ok || time.Now().After(existing.ExpiresAt) {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "not held"})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(existing)
	})

	return httptest.NewServer(mux)
}

func TestHTTPLockClient_AcquireRelease(t *testing.T) {
	srv := mockLockServer(t)
	defer srv.Close()

	c := ServShared.NewHTTPLockClient(srv.URL)

	res, err := c.Acquire("invoice:42", "billing-svc", 5*time.Second)
	if err != nil {
		t.Fatalf("acquire error: %v", err)
	}
	if !res.Acquired {
		t.Fatal("expected lock to be granted")
	}

	// Release
	if err := c.Release("invoice:42", "billing-svc"); err != nil {
		t.Fatalf("release error: %v", err)
	}
}

func TestHTTPLockClient_ConflictReturnsHeldBy(t *testing.T) {
	srv := mockLockServer(t)
	defer srv.Close()

	c := ServShared.NewHTTPLockClient(srv.URL)
	c.Acquire("shared-resource", "svc-a", 10*time.Second)

	res, err := c.Acquire("shared-resource", "svc-b", 5*time.Second)
	if err == nil && res.Acquired {
		t.Fatal("svc-b should not acquire lock held by svc-a")
	}
	// err from HTTPLockClient on 409 — or acquired==false
	if err == nil && res.HeldBy != "svc-a" {
		t.Errorf("held_by should be svc-a, got %q", res.HeldBy)
	}
}

func TestHTTPLockClient_Extend(t *testing.T) {
	srv := mockLockServer(t)
	defer srv.Close()

	c := ServShared.NewHTTPLockClient(srv.URL)
	c.Acquire("job:99", "worker-1", 2*time.Second)

	entry, err := c.Extend("job:99", "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("extend error: %v", err)
	}
	if time.Until(entry.ExpiresAt) < 25*time.Second {
		t.Errorf("expected ~30s remaining, got %v", time.Until(entry.ExpiresAt))
	}
}

func TestHTTPLockClient_Status(t *testing.T) {
	srv := mockLockServer(t)
	defer srv.Close()

	c := ServShared.NewHTTPLockClient(srv.URL)
	c.Acquire("status-key", "svc-x", 5*time.Second)

	entry, err := c.Status("status-key")
	if err != nil {
		t.Fatalf("status error: %v", err)
	}
	if entry == nil || entry.Owner != "svc-x" {
		t.Errorf("expected owner svc-x, got %v", entry)
	}
}

func TestHTTPLockClient_StatusNotHeld(t *testing.T) {
	srv := mockLockServer(t)
	defer srv.Close()

	c := ServShared.NewHTTPLockClient(srv.URL)
	entry, err := c.Status("no-such-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry != nil {
		t.Fatal("expected nil for unheld key")
	}
}

func TestWithLock_RunsFn(t *testing.T) {
	ran := false
	err := ServShared.WithLock(ServShared.NoOpLocker{}, "k", "owner", time.Second, func() error {
		ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ran {
		t.Fatal("fn should have run")
	}
}

func TestWithLockRetry_EventualSuccess(t *testing.T) {
	attempts := 0
	err := ServShared.WithLockRetry(
		ServShared.NoOpLocker{}, "k", "owner",
		time.Second, 3, time.Millisecond,
		func() error {
			attempts++
			return nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", attempts)
	}
}
