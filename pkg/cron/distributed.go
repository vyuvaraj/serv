package cron

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type LeaderElector struct {
	mu            sync.Mutex
	redisClient   *redis.Client
	lockKey       string
	nodeID        string
	leaseDuration time.Duration
	isLeader      bool
	stopChan      chan struct{}
	wg            sync.WaitGroup
}

func NewLeaderElector(redisURL string, lockKey string, leaseDuration time.Duration) *LeaderElector {
	var rdb *redis.Client
	if redisURL != "" {
		opt, err := redis.ParseURL(redisURL)
		if err == nil {
			rdb = redis.NewClient(opt)
		} else {
			log.Printf("Warning: failed to parse redis URL: %v. Running in standalone leader mode.", err)
		}
	}

	// Generate random node ID
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	nodeID := hex.EncodeToString(b)

	return &LeaderElector{
		redisClient:   rdb,
		lockKey:       lockKey,
		nodeID:        nodeID,
		leaseDuration: leaseDuration,
		stopChan:      make(chan struct{}),
	}
}

func (le *LeaderElector) Start() {
	if le.redisClient == nil {
		le.isLeader = true
		log.Println("No Redis configured. Running in standalone LEADER mode.")
		return
	}

	le.wg.Add(1)
	go le.electionLoop()
}

func (le *LeaderElector) Stop() {
	if le.redisClient == nil {
		return
	}
	close(le.stopChan)
	le.wg.Wait()

	// Try release lock if leader
	le.mu.Lock()
	if le.isLeader {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		le.redisClient.Del(ctx, le.lockKey)
	}
	le.mu.Unlock()
}

func (le *LeaderElector) IsLeader() bool {
	le.mu.Lock()
	defer le.mu.Unlock()
	return le.isLeader
}

func (le *LeaderElector) electionLoop() {
	defer le.wg.Done()

	ticker := time.NewTicker(le.leaseDuration / 3)
	defer ticker.Stop()

	// Initial attempt
	le.tryAcquireOrRenew()

	for {
		select {
		case <-le.stopChan:
			return
		case <-ticker.C:
			le.tryAcquireOrRenew()
		}
	}
}

func (le *LeaderElector) tryAcquireOrRenew() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	le.mu.Lock()
	defer le.mu.Unlock()

	if le.isLeader {
		// Renew lease
		ok, err := le.redisClient.Expire(ctx, le.lockKey, le.leaseDuration).Result()
		if err != nil || !ok {
			log.Printf("Node %s: Failed to renew leader lease. Stepping down: %v", le.nodeID, err)
			le.isLeader = false
		}
	} else {
		// Try acquire lock
		ok, err := le.redisClient.SetNX(ctx, le.lockKey, le.nodeID, le.leaseDuration).Result()
		if err != nil {
			log.Printf("Node %s: Error acquiring lock: %v", le.nodeID, err)
			return
		}
		if ok {
			log.Printf("Node %s: Promoted to LEADER", le.nodeID)
			le.isLeader = true
		}
	}
}
