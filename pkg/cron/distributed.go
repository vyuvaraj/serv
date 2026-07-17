package cron

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// LeaderElectionProvider defines dynamic hooks for leader election and task slot locking.
type LeaderElectionProvider interface {
	Start()
	Stop()
	IsLeader() bool
	AcquireJobLock(jobID string, nextRun time.Time) bool
}

// ActiveLeaderElectionProvider is the globally registered leader election provider.
var ActiveLeaderElectionProvider LeaderElectionProvider

// StandaloneLeader is the default fallback provider for OSS (always runs as leader).
type StandaloneLeader struct{}

func (s *StandaloneLeader) Start()                                                    {}
func (s *StandaloneLeader) Stop()                                                     {}
func (s *StandaloneLeader) IsLeader() bool                                            { return true }
func (s *StandaloneLeader) AcquireJobLock(jobID string, nextRun time.Time) bool       { return true }

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

type ServLockElector struct {
	mu            sync.Mutex
	servLockURL   string
	lockKey       string
	nodeID        string
	leaseDuration time.Duration
	isLeader      bool
	stopChan      chan struct{}
	wg            sync.WaitGroup
}

func NewLeaderElector(redisURL string, lockKey string, leaseDuration time.Duration) LeaderElectionProvider {
	if ActiveLeaderElectionProvider != nil {
		return ActiveLeaderElectionProvider
	}

	if os.Getenv("SERV_LOCK_URL") != "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		nodeID := hex.EncodeToString(b)
		return &ServLockElector{
			servLockURL:   os.Getenv("SERV_LOCK_URL"),
			lockKey:       lockKey,
			nodeID:        nodeID,
			leaseDuration: leaseDuration,
			stopChan:      make(chan struct{}),
		}
	}

	// In the OSS core build, if no Redis URL is provided, we use the standalone engine directly
	if redisURL == "" {
		return &StandaloneLeader{}
	}

	var rdb *redis.Client
	opt, err := redis.ParseURL(redisURL)
	if err == nil {
		rdb = redis.NewClient(opt)
	} else {
		log.Printf("Warning: failed to parse redis URL: %v. Running in standalone leader mode.", err)
		return &StandaloneLeader{}
	}

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

// ServLockElector implementation (EI.5)

func (sle *ServLockElector) Start() {
	sle.wg.Add(1)
	go sle.electionLoop()
}

func (sle *ServLockElector) Stop() {
	close(sle.stopChan)
	sle.wg.Wait()

	sle.mu.Lock()
	if sle.isLeader {
		sle.releaseLock(sle.lockKey)
	}
	sle.mu.Unlock()
}

func (sle *ServLockElector) IsLeader() bool {
	sle.mu.Lock()
	defer sle.mu.Unlock()
	return sle.isLeader
}

func (sle *ServLockElector) electionLoop() {
	defer sle.wg.Done()

	ticker := time.NewTicker(sle.leaseDuration / 3)
	defer ticker.Stop()

	sle.tryAcquireOrRenew()

	for {
		select {
		case <-sle.stopChan:
			return
		case <-ticker.C:
			sle.tryAcquireOrRenew()
		}
	}
}

func (sle *ServLockElector) tryAcquireOrRenew() {
	sle.mu.Lock()
	defer sle.mu.Unlock()

	ok := sle.acquireLock(sle.lockKey, sle.leaseDuration)
	if ok {
		if !sle.isLeader {
			log.Printf("[ServLock] Node %s: Promoted to LEADER", sle.nodeID)
			sle.isLeader = true
		}
	} else {
		if sle.isLeader {
			log.Printf("[ServLock] Node %s: Failed to renew leader lease. Stepping down.", sle.nodeID)
			sle.isLeader = false
		}
	}
}

func (sle *ServLockElector) acquireLock(key string, duration time.Duration) bool {
	url := sle.servLockURL + "/api/locks/acquire"
	payload := map[string]interface{}{
		"key":         key,
		"owner":       sle.nodeID,
		"client_id":   sle.nodeID,
		"duration_ms": duration.Milliseconds(),
		"wait_ms":     0,
		"mode":        "exclusive",
	}
	body, _ := json.Marshal(payload)
	
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (sle *ServLockElector) releaseLock(key string) bool {
	url := sle.servLockURL + "/api/locks/release"
	payload := map[string]string{
		"key":   key,
		"owner": sle.nodeID,
	}
	body, _ := json.Marshal(payload)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (sle *ServLockElector) AcquireJobLock(jobID string, nextRun time.Time) bool {
	slotKey := fmt.Sprintf("servcron:job:%s:run:%d", jobID, nextRun.Truncate(time.Minute).Unix())
	return sle.acquireLock(slotKey, 5*time.Minute)
}

// Redis LeaderElector implementation

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
		ok, err := le.redisClient.Expire(ctx, le.lockKey, le.leaseDuration).Result()
		if err != nil || !ok {
			log.Printf("Node %s: Failed to renew leader lease. Stepping down: %v", le.nodeID, err)
			le.isLeader = false
		}
	} else {
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

func (le *LeaderElector) AcquireJobLock(jobID string, nextRun time.Time) bool {
	if le.redisClient == nil {
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	slotKey := fmt.Sprintf("servcron:job:%s:run:%d", jobID, nextRun.Truncate(time.Minute).Unix())

	ok, err := le.redisClient.SetNX(ctx, slotKey, le.nodeID, 5*time.Minute).Result()
	if err != nil {
		log.Printf("Node %s: Error acquiring slot lock for job %s: %v", le.nodeID, jobID, err)
		return false
	}
	return ok
}
