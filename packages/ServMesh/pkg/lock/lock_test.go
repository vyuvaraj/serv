package lock_test

import (
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServMesh/pkg/lock"
)

func TestAcquire_GrantsFreeLock(t *testing.T) {
	s := lock.NewStore(10 * time.Second)
	defer s.Close()

	result := s.Acquire("key1", "svc-a", 5*time.Second)
	if !result.Acquired {
		t.Fatal("expected lock to be granted")
	}
	if result.Lock == nil {
		t.Fatal("expected lock entry in result")
	}
	if result.Lock.Owner != "svc-a" {
		t.Errorf("got owner %q, want %q", result.Lock.Owner, "svc-a")
	}
}

func TestAcquire_BlocksSecondOwner(t *testing.T) {
	s := lock.NewStore(10 * time.Second)
	defer s.Close()

	r1 := s.Acquire("key1", "svc-a", 5*time.Second)
	if !r1.Acquired {
		t.Fatal("first acquire should succeed")
	}

	r2 := s.Acquire("key1", "svc-b", 5*time.Second)
	if r2.Acquired {
		t.Fatal("second owner should NOT acquire held lock")
	}
	if r2.HeldBy != "svc-a" {
		t.Errorf("got held_by=%q, want %q", r2.HeldBy, "svc-a")
	}
}

func TestAcquire_SameOwnerRefreshesTTL(t *testing.T) {
	s := lock.NewStore(10 * time.Second)
	defer s.Close()

	r1 := s.Acquire("key1", "svc-a", 5*time.Second)
	if !r1.Acquired {
		t.Fatal("first acquire should succeed")
	}
	first := r1.Lock.ExpiresAt

	// Re-acquire with longer TTL
	r2 := s.Acquire("key1", "svc-a", 10*time.Second)
	if !r2.Acquired {
		t.Fatal("re-acquire by same owner should succeed")
	}
	if !r2.Lock.ExpiresAt.After(first) {
		t.Errorf("expected extended expiry, got %v <= %v", r2.Lock.ExpiresAt, first)
	}
}

func TestRelease_ByOwner(t *testing.T) {
	s := lock.NewStore(10 * time.Second)
	defer s.Close()

	s.Acquire("key1", "svc-a", 5*time.Second)
	ok := s.Release("key1", "svc-a")
	if !ok {
		t.Fatal("release by owner should return true")
	}

	// Now another owner should get it
	r := s.Acquire("key1", "svc-b", 5*time.Second)
	if !r.Acquired {
		t.Fatal("lock should be available after release")
	}
}

func TestRelease_WrongOwnerFails(t *testing.T) {
	s := lock.NewStore(10 * time.Second)
	defer s.Close()

	s.Acquire("key1", "svc-a", 5*time.Second)
	ok := s.Release("key1", "svc-b") // wrong owner
	if ok {
		t.Fatal("releasing with wrong owner should return false")
	}
}

func TestRelease_NotHeldFails(t *testing.T) {
	s := lock.NewStore(10 * time.Second)
	defer s.Close()

	ok := s.Release("nonexistent", "svc-a")
	if ok {
		t.Fatal("releasing unheld lock should return false")
	}
}

func TestExtend_RefreshesTTL(t *testing.T) {
	s := lock.NewStore(10 * time.Second)
	defer s.Close()

	s.Acquire("key1", "svc-a", 2*time.Second)
	entry, ok := s.Extend("key1", "svc-a", 30*time.Second)
	if !ok {
		t.Fatal("extend should succeed for holder")
	}
	if time.Until(entry.ExpiresAt) < 25*time.Second {
		t.Errorf("expected ~30s remaining, got %v", time.Until(entry.ExpiresAt))
	}
}

func TestExtend_WrongOwnerFails(t *testing.T) {
	s := lock.NewStore(10 * time.Second)
	defer s.Close()

	s.Acquire("key1", "svc-a", 5*time.Second)
	_, ok := s.Extend("key1", "svc-b", 10*time.Second)
	if ok {
		t.Fatal("extend by non-owner should fail")
	}
}

func TestStatus_HeldLock(t *testing.T) {
	s := lock.NewStore(10 * time.Second)
	defer s.Close()

	s.Acquire("key1", "svc-a", 5*time.Second)
	entry, ok := s.Status("key1")
	if !ok {
		t.Fatal("status of held lock should return true")
	}
	if entry.Owner != "svc-a" {
		t.Errorf("got owner %q, want %q", entry.Owner, "svc-a")
	}
}

func TestStatus_NotHeld(t *testing.T) {
	s := lock.NewStore(10 * time.Second)
	defer s.Close()

	_, ok := s.Status("nothing")
	if ok {
		t.Fatal("status of unheld key should return false")
	}
}

func TestExpiry_LockExpires(t *testing.T) {
	s := lock.NewStore(50 * time.Millisecond)
	defer s.Close()

	s.Acquire("key1", "svc-a", 50*time.Millisecond)
	time.Sleep(100 * time.Millisecond)

	// After expiry another owner should be able to take it
	r := s.Acquire("key1", "svc-b", 5*time.Second)
	if !r.Acquired {
		t.Fatal("expired lock should be acquirable by new owner")
	}
}

func TestList_ReturnsHeld(t *testing.T) {
	s := lock.NewStore(10 * time.Second)
	defer s.Close()

	s.Acquire("alpha", "svc-a", 5*time.Second)
	s.Acquire("beta", "svc-b", 5*time.Second)

	entries := s.List()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestList_ExcludesExpired(t *testing.T) {
	s := lock.NewStore(50 * time.Millisecond)
	defer s.Close()

	s.Acquire("short", "svc-a", 50*time.Millisecond)
	s.Acquire("long", "svc-b", 10*time.Second)

	time.Sleep(100 * time.Millisecond)

	entries := s.List()
	for _, e := range entries {
		if e.Key == "short" {
			t.Error("expired lock should not appear in List()")
		}
	}
}
