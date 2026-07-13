package cache

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisCache struct {
	client     *redis.Client
	ctx        context.Context
	fallback   *InMemoryCache
	mu         sync.RWMutex
	isOffline  bool
	redisURL   string
}

func NewRedisCache(url string) (*RedisCache, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	
	opts.PoolSize = 50
	opts.MinIdleConns = 5
	opts.ConnMaxIdleTime = 5 * time.Minute
	opts.ConnMaxLifetime = 30 * time.Minute
	opts.DialTimeout = 200 * time.Millisecond
	opts.ReadTimeout = 200 * time.Millisecond
	opts.WriteTimeout = 200 * time.Millisecond

	client := redis.NewClient(opts)
	
	fallback := NewInMemoryCache(100 * time.Millisecond)

	rc := &RedisCache{
		client:    client,
		ctx:       context.Background(),
		fallback:  fallback,
		redisURL:  url,
		isOffline: false,
	}

	go rc.monitorFailover()

	return rc, nil
}

func (r *RedisCache) monitorFailover() {
	ticker := time.NewTicker(500 * time.Millisecond)
	for range ticker.C {
		err := r.client.Ping(r.ctx).Err()
		r.mu.Lock()
		if err != nil {
			if !r.isOffline {
				log.Println("[Redis Cache] Redis connection offline! Falling back to in-memory cache...")
				r.isOffline = true
			}
		} else {
			if r.isOffline {
				log.Println("[Redis Cache] Redis connection restored! Syncing fallback keys...")
				r.isOffline = false
				go r.syncFallbackToRedis()
			}
		}
		r.mu.Unlock()
	}
}

func (r *RedisCache) syncFallbackToRedis() {
	keys := r.fallback.Keys()
	for _, k := range keys {
		val, exists, err := r.fallback.Get(k)
		if err == nil && exists {
			r.Set(k, val, 5*time.Minute)
		}
	}
	r.fallback.Clear()
}

func (r *RedisCache) Get(key string) (interface{}, bool, error) {
	r.mu.RLock()
	offline := r.isOffline
	r.mu.RUnlock()

	if offline {
		return r.fallback.Get(key)
	}

	val, err := r.client.Get(r.ctx, key).Result()
	if err == redis.Nil {
		return nil, false, nil
	} else if err != nil {
		r.mu.Lock()
		r.isOffline = true
		r.mu.Unlock()
		return r.fallback.Get(key)
	}

	var parsed interface{}
	if err := json.Unmarshal([]byte(val), &parsed); err == nil {
		return parsed, true, nil
	}
	return val, true, nil
}

func (r *RedisCache) Set(key string, value interface{}, ttl time.Duration) error {
	r.mu.RLock()
	offline := r.isOffline
	r.mu.RUnlock()

	if offline {
		return r.fallback.Set(key, value, ttl)
	}

	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	err = r.client.Set(r.ctx, key, string(b), ttl).Err()
	if err != nil {
		r.mu.Lock()
		r.isOffline = true
		r.mu.Unlock()
		return r.fallback.Set(key, value, ttl)
	}
	return nil
}

func (r *RedisCache) Delete(key string) error {
	r.mu.RLock()
	offline := r.isOffline
	r.mu.RUnlock()

	if offline {
		return r.fallback.Delete(key)
	}

	err := r.client.Del(r.ctx, key).Err()
	if err != nil {
		r.mu.Lock()
		r.isOffline = true
		r.mu.Unlock()
		return r.fallback.Delete(key)
	}
	return nil
}

func (r *RedisCache) Clear() error {
	r.mu.RLock()
	offline := r.isOffline
	r.mu.RUnlock()

	if offline {
		return r.fallback.Clear()
	}

	err := r.client.FlushDB(r.ctx).Err()
	if err != nil {
		r.mu.Lock()
		r.isOffline = true
		r.mu.Unlock()
		return r.fallback.Clear()
	}
	return nil
}

func (r *RedisCache) DeletePattern(pattern string) error {
	r.mu.RLock()
	offline := r.isOffline
	r.mu.RUnlock()

	if offline {
		return r.fallback.DeletePattern(pattern)
	}

	keys, err := r.client.Keys(r.ctx, pattern).Result()
	if err != nil {
		r.mu.Lock()
		r.isOffline = true
		r.mu.Unlock()
		return r.fallback.DeletePattern(pattern)
	}
	if len(keys) > 0 {
		err = r.client.Del(r.ctx, keys...).Err()
		if err != nil {
			return r.fallback.DeletePattern(pattern)
		}
	}
	return nil
}

func (r *RedisCache) Keys() []string {
	r.mu.RLock()
	offline := r.isOffline
	r.mu.RUnlock()

	if offline {
		return r.fallback.Keys()
	}

	keys, err := r.client.Keys(r.ctx, "*").Result()
	if err != nil {
		return r.fallback.Keys()
	}
	return keys
}
