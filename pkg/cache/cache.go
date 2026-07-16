package cache

import (
	"bytes"
	"container/list"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"strconv"
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
	key        string
	value      interface{}
	expiration time.Time
}

type InMemoryCache struct {
	mu        sync.RWMutex
	items     map[string]*list.Element
	evictList *list.List
	maxKeys   int
	cleanup   time.Duration
	sf        Group
}

func NewInMemoryCache(cleanupInterval time.Duration) *InMemoryCache {
	maxKeys := 10000
	if envMax := os.Getenv("SERV_CACHE_MAX_KEYS"); envMax != "" {
		if val, err := strconv.Atoi(envMax); err == nil && val > 0 {
			maxKeys = val
		}
	}

	if cleanupInterval < 10*time.Millisecond {
		cleanupInterval = 100 * time.Millisecond
	}

	c := &InMemoryCache{
		items:     make(map[string]*list.Element),
		evictList: list.New(),
		maxKeys:   maxKeys,
		cleanup:   cleanupInterval,
	}
	go c.startEvictionLoop()
	return c
}

func (c *InMemoryCache) Get(key string) (interface{}, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, exists := c.items[key]
	if !exists {
		if backend := os.Getenv("SERV_CACHE_BACKEND_DB"); backend != "" {
			// Release lock temporarily for HTTP request
			c.mu.Unlock()
			val, err := c.sf.Do(key, func() (interface{}, error) {
				return c.fetchFromBackend(key)
			})
			c.mu.Lock()
			if err == nil && val != nil {
				c.setLocalNoLock(key, val, 1*time.Minute)
				return val, true, nil
			}
		}
		return nil, false, nil
	}

	entry := elem.Value.(*cacheEntry)
	if !entry.expiration.IsZero() && time.Now().After(entry.expiration) {
		c.evictList.Remove(elem)
		delete(c.items, key)

		if backend := os.Getenv("SERV_CACHE_BACKEND_DB"); backend != "" {
			c.mu.Unlock()
			val, err := c.sf.Do(key, func() (interface{}, error) {
				return c.fetchFromBackend(key)
			})
			c.mu.Lock()
			if err == nil && val != nil {
				c.setLocalNoLock(key, val, 1*time.Minute)
				return val, true, nil
			}
		}
		return nil, false, nil
	}

	c.evictList.MoveToFront(elem)
	return entry.value, true, nil
}

func (c *InMemoryCache) setLocalNoLock(key string, value interface{}, ttl time.Duration) {
	var expiration time.Time
	if ttl > 0 {
		expiration = time.Now().Add(ttl)
	}

	if elem, exists := c.items[key]; exists {
		c.evictList.MoveToFront(elem)
		entry := elem.Value.(*cacheEntry)
		entry.value = value
		entry.expiration = expiration
		return
	}

	entry := &cacheEntry{
		key:        key,
		value:      value,
		expiration: expiration,
	}
	elem := c.evictList.PushFront(entry)
	c.items[key] = elem

	if c.maxKeys > 0 && c.evictList.Len() > c.maxKeys {
		c.evictOldestNoLock()
	}
}

func (c *InMemoryCache) evictOldestNoLock() {
	elem := c.evictList.Back()
	if elem != nil {
		c.evictList.Remove(elem)
		entry := elem.Value.(*cacheEntry)
		delete(c.items, entry.key)
	}
}

func (c *InMemoryCache) Set(key string, value interface{}, ttl time.Duration) error {
	c.mu.Lock()
	c.setLocalNoLock(key, value, ttl)
	c.mu.Unlock()

	if backend := os.Getenv("SERV_CACHE_BACKEND_DB"); backend != "" {
		go c.writeToBackend(key, value)
	}
	return nil
}

func (c *InMemoryCache) Delete(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, exists := c.items[key]; exists {
		c.evictList.Remove(elem)
		delete(c.items, key)
	}
	return nil
}

func (c *InMemoryCache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*list.Element)
	c.evictList.Init()
	return nil
}

func (c *InMemoryCache) DeletePattern(pattern string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, elem := range c.items {
		matched, err := path.Match(pattern, k)
		if err == nil && matched {
			c.evictList.Remove(elem)
			delete(c.items, k)
		} else if strings.HasSuffix(pattern, "*") {
			prefix := strings.TrimSuffix(pattern, "*")
			if strings.HasPrefix(k, prefix) {
				c.evictList.Remove(elem)
				delete(c.items, k)
			}
		} else if k == pattern {
			c.evictList.Remove(elem)
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
	for k, elem := range c.items {
		entry := elem.Value.(*cacheEntry)
		if !entry.expiration.IsZero() && now.After(entry.expiration) {
			c.evictList.Remove(elem)
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

// Custom singleflight implementation
type call struct {
	wg  sync.WaitGroup
	val interface{}
	err error
}

type Group struct {
	mu sync.Mutex
	m  map[string]*call
}

func (g *Group) Do(key string, fn func() (interface{}, error)) (interface{}, error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*call)
	}
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := new(call)
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()

	return c.val, c.err
}

