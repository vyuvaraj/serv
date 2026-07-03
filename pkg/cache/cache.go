package cache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

type Cache interface {
	Get(key string) (interface{}, bool, error)
	Set(key string, value interface{}, ttl time.Duration) error
	Delete(key string) error
	Clear() error
	DeletePattern(pattern string) error
}

type cacheEntry struct {
	value      interface{}
	expiration time.Time
}

type InMemoryCache struct {
	mu      sync.RWMutex
	items   map[string]cacheEntry
	cleanup time.Duration
}

func NewInMemoryCache(cleanupInterval time.Duration) *InMemoryCache {
	c := &InMemoryCache{
		items:   make(map[string]cacheEntry),
		cleanup: cleanupInterval,
	}
	go c.startEvictionLoop()
	return c
}

func (c *InMemoryCache) Get(key string) (interface{}, bool, error) {
	c.mu.RLock()
	entry, exists := c.items[key]
	c.mu.RUnlock()

	if !exists {
		if backend := os.Getenv("SERV_CACHE_BACKEND_DB"); backend != "" {
			val, err := c.fetchFromBackend(key)
			if err == nil && val != nil {
				c.setLocal(key, val, 1*time.Minute)
				return val, true, nil
			}
		}
		return nil, false, nil
	}

	if !entry.expiration.IsZero() && time.Now().After(entry.expiration) {
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		
		if backend := os.Getenv("SERV_CACHE_BACKEND_DB"); backend != "" {
			val, err := c.fetchFromBackend(key)
			if err == nil && val != nil {
				c.setLocal(key, val, 1*time.Minute)
				return val, true, nil
			}
		}
		return nil, false, nil
	}

	return entry.value, true, nil
}

func (c *InMemoryCache) setLocal(key string, value interface{}, ttl time.Duration) {
	var expiration time.Time
	if ttl > 0 {
		expiration = time.Now().Add(ttl)
	}
	c.mu.Lock()
	c.items[key] = cacheEntry{
		value:      value,
		expiration: expiration,
	}
	c.mu.Unlock()
}

func (c *InMemoryCache) Set(key string, value interface{}, ttl time.Duration) error {
	c.setLocal(key, value, ttl)

	if backend := os.Getenv("SERV_CACHE_BACKEND_DB"); backend != "" {
		go c.writeToBackend(key, value)
	}
	return nil
}

func (c *InMemoryCache) Delete(key string) error {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
	return nil
}

func (c *InMemoryCache) Clear() error {
	c.mu.Lock()
	c.items = make(map[string]cacheEntry)
	c.mu.Unlock()
	return nil
}

func (c *InMemoryCache) DeletePattern(pattern string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k := range c.items {
		matched, err := path.Match(pattern, k)
		if err == nil && matched {
			delete(c.items, k)
		} else if strings.HasSuffix(pattern, "*") {
			prefix := strings.TrimSuffix(pattern, "*")
			if strings.HasPrefix(k, prefix) {
				delete(c.items, k)
			}
		} else if k == pattern {
			delete(c.items, k)
		}
	}
	return nil
}

func (c *InMemoryCache) fetchFromBackend(key string) (interface{}, error) {
	backend := os.Getenv("SERV_CACHE_BACKEND_DB")
	url := fmt.Sprintf("%s/%s", strings.TrimSuffix(backend, "/"), key)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var val interface{}
		if err := json.NewDecoder(resp.Body).Decode(&val); err == nil {
			return val, nil
		}
	}
	return nil, fmt.Errorf("backend returned status %d", resp.StatusCode)
}

func (c *InMemoryCache) writeToBackend(key string, value interface{}) {
	backend := os.Getenv("SERV_CACHE_BACKEND_DB")
	url := fmt.Sprintf("%s/%s", strings.TrimSuffix(backend, "/"), key)
	data, err := json.Marshal(value)
	if err != nil {
		return
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (c *InMemoryCache) EvictExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for k, v := range c.items {
		if !v.expiration.IsZero() && now.After(v.expiration) {
			delete(c.items, k)
		}
	}
}

func (c *InMemoryCache) startEvictionLoop() {
	ticker := time.NewTicker(c.cleanup)
	for range ticker.C {
		c.EvictExpired()
	}
}

func (c *InMemoryCache) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	keys := make([]string, 0, len(c.items))
	for k := range c.items {
		keys = append(keys, k)
	}
	return keys
}
