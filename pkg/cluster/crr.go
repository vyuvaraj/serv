package cluster

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"servstore/pkg/storage"
)

type CRRJob struct {
	Bucket    string
	Key       string
	VersionID string
	Delete    bool
}

type CRRManager struct {
	mu         sync.RWMutex
	store      storage.StorageEngine
	clusterMgr *MembershipManager
	queue      chan CRRJob
	stopChan   chan struct{}
	running    bool
	accessKey  string
	secretKey  string
}

func NewCRRManager(store storage.StorageEngine, clusterMgr *MembershipManager, accessKey, secretKey string) *CRRManager {
	return &CRRManager{
		store:      store,
		clusterMgr: clusterMgr,
		queue:      make(chan CRRJob, 1000),
		stopChan:   make(chan struct{}),
		accessKey:  accessKey,
		secretKey:  secretKey,
	}
}

func (c *CRRManager) Start(ctx context.Context) {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.mu.Unlock()

	slog.Info("Starting Cross-Region Replication (CRR) Manager...")
	go c.workerLoop(ctx)
}

func (c *CRRManager) Stop() {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.running = false
	close(c.stopChan)
	c.mu.Unlock()
}

func (c *CRRManager) Enqueue(job CRRJob) {
	select {
	case c.queue <- job:
	default:
		slog.Warn("CRR queue is full, dropping replication job", "bucket", job.Bucket, "key", job.Key)
	}
}

func (c *CRRManager) workerLoop(ctx context.Context) {
	for {
		select {
		case <-c.stopChan:
			return
		case <-ctx.Done():
			return
		case job := <-c.queue:
			if err := c.replicateJob(ctx, job); err != nil {
				slog.Error("Failed to process CRR replication job", "bucket", job.Bucket, "key", job.Key, "error", err)
				// Retry by enqueuing again after a delay
				go func(j CRRJob) {
					time.Sleep(5 * time.Second)
					c.Enqueue(j)
				}(job)
			}
		}
	}
}

func (c *CRRManager) replicateJob(ctx context.Context, job CRRJob) error {
	if c.clusterMgr == nil {
		return nil
	}

	// Find remote regions/nodes
	var targets []string
	c.clusterMgr.mu.RLock()
	localRegion := c.clusterMgr.localNode.Region
	for _, peer := range c.clusterMgr.peers {
		if peer.Status == "online" && peer.Region != "" && peer.Region != localRegion {
			targets = append(targets, peer.Address)
		}
	}
	c.clusterMgr.mu.RUnlock()

	if len(targets) == 0 {
		return nil // No remote regions configured or online
	}

	// We only need to replicate to one gateway per region, let's replicate to the first online target
	targetAddr := targets[0]

	if job.Delete {
		return c.replicateDeleteToNode(ctx, job.Bucket, job.Key, job.VersionID, targetAddr)
	}

	return c.replicateVersionToNode(ctx, job.Bucket, job.Key, job.VersionID, targetAddr)
}

func (c *CRRManager) replicateVersionToNode(ctx context.Context, bucket, key, versionID string, addr string) error {
	reader, obj, err := c.store.GetObject(ctx, bucket, key, versionID)
	if err != nil {
		return err
	}
	defer reader.Close()

	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	targetURL := fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(addr, "/"), bucket, key)
	req, err := http.NewRequestWithContext(ctx, "PUT", targetURL, reader)
	if err != nil {
		return err
	}
	req.ContentLength = obj.Size
	req.Header.Set("Content-Type", obj.ContentType)
	req.Header.Set("X-ServStore-Replicated", "true")
	req.Header.Set("X-ServStore-Region-Source", c.clusterMgr.localNode.Region)
	req.Header.Set("X-ServStore-Version-Id", versionID)

	req.SetBasicAuth(c.accessKey, c.secretKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remote node status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (c *CRRManager) replicateDeleteToNode(ctx context.Context, bucket, key, versionID string, addr string) error {
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	targetURL := fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(addr, "/"), bucket, key)
	if versionID != "" && versionID != "null" {
		targetURL += "?versionId=" + versionID
	}
	req, err := http.NewRequestWithContext(ctx, "DELETE", targetURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-ServStore-Replicated", "true")
	req.Header.Set("X-ServStore-Region-Source", c.clusterMgr.localNode.Region)

	req.SetBasicAuth(c.accessKey, c.secretKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remote node status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
