package cache

import (
	"context"
	"container/list"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func TestNewInMemoryCacheDefault(t *testing.T) {
	c := NewInMemoryCache(1 * time.Second)
	if c.maxKeys != 10000 {
		t.Errorf("expected default maxKeys 10000, got %d", c.maxKeys)
	}
}

func TestNewInMemoryCacheMaxKeysEnv(t *testing.T) {
	os.Setenv("SERV_CACHE_MAX_KEYS", "42")
	defer os.Unsetenv("SERV_CACHE_MAX_KEYS")
	c := NewInMemoryCache(1 * time.Second)
	if c.maxKeys != 42 {
		t.Errorf("expected maxKeys 42 from env, got %d", c.maxKeys)
	}
}

func TestNewInMemoryCacheInvalidMaxKeysEnv(t *testing.T) {
	os.Setenv("SERV_CACHE_MAX_KEYS", "invalid")
	defer os.Unsetenv("SERV_CACHE_MAX_KEYS")
	c := NewInMemoryCache(1 * time.Second)
	if c.maxKeys != 10000 {
		t.Errorf("expected fallback maxKeys 10000, got %d", c.maxKeys)
	}
}

func TestInMemoryCacheGetMiss(t *testing.T) {
	c := &InMemoryCache{
		items:     make(map[string]*list.Element),
		evictList: list.New(),
		maxKeys:   10,
	}
	_, found, err := c.Get("absent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected found to be false")
	}
}

func TestInMemoryCacheGetExpired(t *testing.T) {
	c := &InMemoryCache{
		items:     make(map[string]*list.Element),
		evictList: list.New(),
		maxKeys:   10,
	}
	entry := &cacheEntry{
		key:        "expired",
		value:      "val",
		expiration: time.Now().Add(-1 * time.Minute),
	}
	elem := c.evictList.PushFront(entry)
	c.items["expired"] = elem

	_, found, _ := c.Get("expired")
	if found {
		t.Error("expected expired item to be treated as miss")
	}
}

func TestInMemoryCacheGetValid(t *testing.T) {
	c := &InMemoryCache{
		items:     make(map[string]*list.Element),
		evictList: list.New(),
		maxKeys:   10,
	}
	entry := &cacheEntry{
		key:        "valid",
		value:      "val",
		expiration: time.Now().Add(1 * time.Minute),
	}
	elem := c.evictList.PushFront(entry)
	c.items["valid"] = elem

	val, found, _ := c.Get("valid")
	if !found || val != "val" {
		t.Errorf("expected val, got %v", val)
	}
}

func TestInMemoryCacheSetUpdate(t *testing.T) {
	c := &InMemoryCache{
		items:     make(map[string]*list.Element),
		evictList: list.New(),
		maxKeys:   10,
	}
	_ = c.Set("key", "val1", 0)
	_ = c.Set("key", "val2", 0)

	val, _, _ := c.Get("key")
	if val != "val2" {
		t.Errorf("expected updated value val2, got %v", val)
	}
}

func TestInMemoryCacheDeleteAbsent(t *testing.T) {
	c := &InMemoryCache{
		items:     make(map[string]*list.Element),
		evictList: list.New(),
		maxKeys:   10,
	}
	err := c.Delete("absent")
	if err != nil {
		t.Errorf("expected no error deleting absent key, got %v", err)
	}
}

func TestInMemoryCacheClearEmpty(t *testing.T) {
	c := &InMemoryCache{
		items:     make(map[string]*list.Element),
		evictList: list.New(),
		maxKeys:   10,
	}
	_ = c.Clear()
	if len(c.items) != 0 || c.evictList.Len() != 0 {
		t.Error("expected empty cache after clear")
	}
}

func TestInMemoryCacheDeletePatternExact(t *testing.T) {
	c := &InMemoryCache{
		items:     make(map[string]*list.Element),
		evictList: list.New(),
		maxKeys:   10,
	}
	_ = c.Set("exact", "val", 0)
	_ = c.DeletePattern("exact")
	_, found, _ := c.Get("exact")
	if found {
		t.Error("expected exact pattern deleted")
	}
}

func TestInMemoryCacheDeletePatternPrefix(t *testing.T) {
	c := &InMemoryCache{
		items:     make(map[string]*list.Element),
		evictList: list.New(),
		maxKeys:   10,
	}
	_ = c.Set("prefix_test", "val", 0)
	_ = c.DeletePattern("prefix_*")
	_, found, _ := c.Get("prefix_test")
	if found {
		t.Error("expected prefix pattern deleted")
	}
}

func TestInMemoryCacheEvictExpired(t *testing.T) {
	c := &InMemoryCache{
		items:     make(map[string]*list.Element),
		evictList: list.New(),
		maxKeys:   10,
	}
	_ = c.Set("exp", "val", 1*time.Millisecond)
	time.Sleep(2 * time.Millisecond)
	c.EvictExpired()
	_, found, _ := c.Get("exp")
	if found {
		t.Error("expected key to be evicted")
	}
}

func TestRedisCacheParseURLError(t *testing.T) {
	_, err := NewRedisCache("invalid-url")
	if err == nil {
		t.Error("expected ParseURL error for invalid URL")
	}
}

func TestNewRedisCacheValid(t *testing.T) {
	// Since we mock/connect, redis ParseURL will pass with redis:// syntax but connection fails when pinged.
	// But ParseURL doesn't ping. So NewRedisCache should pass without error.
	rc, err := NewRedisCache("redis://localhost:6379")
	if err != nil {
		t.Fatalf("unexpected ParseURL error: %v", err)
	}
	if rc.client == nil {
		t.Error("expected client to be initialized")
	}
}

func TestRedisCacheGetMiss(t *testing.T) {
	rc := &RedisCache{
		client:    nil,
		ctx:       context.Background(),
		fallback:  NewInMemoryCache(10 * time.Millisecond),
		isOffline: true,
	}
	// Offline mode should fallback to in-memory gracefully without nil pointer dereference
	_, found, err := rc.Get("key")
	if err != nil {
		t.Fatalf("unexpected offline fallback error: %v", err)
	}
	if found {
		t.Error("expected key to be absent in fallback")
	}
}

func TestRedisCacheDeleteError(t *testing.T) {
	rc := &RedisCache{
		client:    nil,
		ctx:       context.Background(),
		fallback:  NewInMemoryCache(10 * time.Millisecond),
		isOffline: true,
	}
	err := rc.Delete("key")
	if err != nil {
		t.Errorf("unexpected offline fallback delete error: %v", err)
	}
}

func TestCacheTTLAccuraceTiming(t *testing.T) {
	c := NewInMemoryCache(10 * time.Millisecond)
	_ = c.Set("timing_key", "timing_val", 50*time.Millisecond)

	// Get before expiration
	val, found, err := c.Get("timing_key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || val != "timing_val" {
		t.Errorf("expected value to exist, got: %v", val)
	}

	// Sleep past expiration (50ms + 100ms margin)
	time.Sleep(150 * time.Millisecond)

	val2, found2, err2 := c.Get("timing_key")
	if err2 != nil {
		t.Fatalf("unexpected error: %v", err2)
	}
	if found2 {
		t.Errorf("expected value to have expired, got: %v", val2)
	}
}

func TestRedisFailoverFallback(t *testing.T) {
	rc, err := NewRedisCache("redis://localhost:6379")
	if err != nil {
		t.Fatalf("failed to init Redis cache: %v", err)
	}

	// Force offline state to simulate Redis connection failure
	rc.mu.Lock()
	rc.isOffline = true
	rc.mu.Unlock()

	// Set and Get should fall back to local memory cache
	err = rc.Set("failover_key", "failover_val", 5*time.Minute)
	if err != nil {
		t.Fatalf("Set failed under failover fallback: %v", err)
	}

	val, found, err := rc.Get("failover_key")
	if err != nil {
		t.Fatalf("Get failed under failover fallback: %v", err)
	}
	if !found || val != "failover_val" {
		t.Errorf("expected to retrieve fallback cached item, got %v", val)
	}
}

func Test100GoroutineCacheStress(t *testing.T) {
	c := NewInMemoryCache(50 * time.Millisecond)
	var wg sync.WaitGroup

	// Run 100 concurrent workers performing Set, Get, and Delete
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("stress_key_%d", id%10)
			_ = c.Set(key, id, 10*time.Millisecond)
			_, _, _ = c.Get(key)
			_ = c.Delete(key)
		}(i)
	}
	wg.Wait()
}

