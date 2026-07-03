package cache

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisCache struct {
	client *redis.Client
	ctx    context.Context
}

func NewRedisCache(url string) (*RedisCache, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	
	// PS.1: Configure adaptive connection pool tuning and automated invalidation
	opts.PoolSize = 50
	opts.MinIdleConns = 5
	opts.ConnMaxIdleTime = 5 * time.Minute
	opts.ConnMaxLifetime = 30 * time.Minute

	client := redis.NewClient(opts)
	return &RedisCache{
		client: client,
		ctx:    context.Background(),
	}, nil
}

func (r *RedisCache) Get(key string) (interface{}, bool, error) {
	val, err := r.client.Get(r.ctx, key).Result()
	if err == redis.Nil {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}

	var parsed interface{}
	if err := json.Unmarshal([]byte(val), &parsed); err == nil {
		return parsed, true, nil
	}
	return val, true, nil
}

func (r *RedisCache) Set(key string, value interface{}, ttl time.Duration) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return r.client.Set(r.ctx, key, string(b), ttl).Err()
}

func (r *RedisCache) Delete(key string) error {
	return r.client.Del(r.ctx, key).Err()
}

func (r *RedisCache) Clear() error {
	return r.client.FlushDB(r.ctx).Err()
}

func (r *RedisCache) DeletePattern(pattern string) error {
	keys, err := r.client.Keys(r.ctx, pattern).Result()
	if err != nil {
		return err
	}
	if len(keys) > 0 {
		return r.client.Del(r.ctx, keys...).Err()
	}
	return nil
}

func (r *RedisCache) Keys() []string {
	keys, err := r.client.Keys(r.ctx, "*").Result()
	if err != nil {
		return []string{}
	}
	return keys
}
