package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"
)

type HealingManager struct {
	store             storage.StorageEngine
	clusterMgr        *MembershipManager
	replicationFactor int
	accessKey         string
	secretKey         string
}

func NewHealingManager(store storage.StorageEngine, clusterMgr *MembershipManager, replicationFactor int, accessKey, secretKey string) *HealingManager {
	if replicationFactor <= 0 {
		replicationFactor = 2
	}
	return &HealingManager{
		store:             store,
		clusterMgr:        clusterMgr,
		replicationFactor: replicationFactor,
		accessKey:         accessKey,
		secretKey:         secretKey,
	}
}

func (hm *HealingManager) Start(ctx context.Context, interval time.Duration) {
	slog.Info("Starting cluster healing and rebalancing worker...", "interval", interval)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := hm.RunHealingCycle(ctx); err != nil {
					slog.Error("Error during healing cycle", "error", err)
				}
			}
		}
	}()
}

func (hm *HealingManager) RunHealingCycle(ctx context.Context) error {
	if hm.clusterMgr == nil || hm.clusterMgr.Ring() == nil {
		return nil
	}

	// 1. Get all local keys from storage
	keys, err := hm.store.ListLocalKeys(ctx)
	if err != nil {
		return fmt.Errorf("list local keys: %w", err)
	}

	ring := hm.clusterMgr.Ring()
	localNodeID := hm.clusterMgr.LocalNodeID()

	for _, localKey := range keys {
		ringKey := localKey.Bucket + "/" + localKey.Key

		// 2. Find the top N owner nodes on the ring
		owners, err := ring.GetNodes(ringKey, hm.replicationFactor)
		if err != nil {
			slog.Warn("Failed to get owners for key", "key", ringKey, "error", err)
			continue
		}

		// Check if local node is in the list of owners
		isLocalOwner := false
		for _, owner := range owners {
			if owner == localNodeID {
				isLocalOwner = true
				break
			}
		}

		if !isLocalOwner {
			// REBALANCING HANDOFF
			// Local node no longer owns this key (e.g. new node joined).
			// Replicate it to the first online owner, then delete locally.
			var targetNode string
			var targetAddr string
			for _, owner := range owners {
				if hm.clusterMgr.IsNodeOnline(owner) {
					addr, exists := hm.clusterMgr.GetNodeAddress(owner)
					if exists {
						targetNode = owner
						targetAddr = addr
						break
					}
				}
			}

			if targetNode != "" {
				slog.Info("Rebalancing key: local node is no longer an owner, handing off", "key", ringKey, "to_node", targetNode)
				GlobalHub.Publish(ClusterEvent{
					Type:   "rebalance_progress",
					NodeID: localNodeID,
					Status: "rebalancing",
					Details: map[string]interface{}{
						"key":     ringKey,
						"to_node": targetNode,
						"action":  "handoff_start",
					},
				})
				if err := hm.replicateLocalKeyToRemoteNode(ctx, localKey.Bucket, localKey.Key, targetAddr); err == nil {
					if err := hm.purgeLocalKey(ctx, localKey.Bucket, localKey.Key); err != nil {
						slog.Error("Failed to purge rebalanced key locally", "key", ringKey, "error", err)
					} else {
						slog.Info("Successfully rebalanced and purged key locally", "key", ringKey)
						GlobalHub.Publish(ClusterEvent{
							Type:   "rebalance_progress",
							NodeID: localNodeID,
							Status: "rebalanced",
							Details: map[string]interface{}{
								"key":     ringKey,
								"to_node": targetNode,
								"action":  "handoff_complete",
							},
						})
					}
				} else {
					slog.Error("Failed to replicate key during rebalancing", "key", ringKey, "to_node", targetNode, "error", err)
				}
			}
		} else {
			// AUTO-HEALING
			// Local node is an owner. Ensure other online owners have their replicas.
			for _, owner := range owners {
				if owner == localNodeID {
					continue
				}

				if hm.clusterMgr.IsNodeOnline(owner) {
					addr, exists := hm.clusterMgr.GetNodeAddress(owner)
					if !exists {
						continue
					}

					hasKey, err := hm.checkRemoteKeyExists(ctx, localKey.Bucket, localKey.Key, addr)
					if err != nil {
						slog.Warn("Failed to check if remote node has key", "node", owner, "key", ringKey, "error", err)
						continue
					}

					if !hasKey {
						slog.Info("Auto-healing key: replicating missing replica", "key", ringKey, "to_node", owner)
						GlobalHub.Publish(ClusterEvent{
							Type:   "rebalance_progress",
							NodeID: localNodeID,
							Status: "healing",
							Details: map[string]interface{}{
								"key":     ringKey,
								"to_node": owner,
								"action":  "healing_start",
							},
						})
						if err := hm.replicateLocalKeyToRemoteNode(ctx, localKey.Bucket, localKey.Key, addr); err != nil {
							slog.Error("Failed to replicate key during auto-healing", "key", ringKey, "to_node", owner, "error", err)
						} else {
							slog.Info("Successfully auto-healed key", "key", ringKey, "to_node", owner)
							GlobalHub.Publish(ClusterEvent{
								Type:   "rebalance_progress",
								NodeID: localNodeID,
								Status: "healed",
								Details: map[string]interface{}{
									"key":     ringKey,
									"to_node": owner,
									"action":  "healing_complete",
								},
							})
						}
					}
				}
			}
		}
	}

	return nil
}

func (hm *HealingManager) replicateLocalKeyToRemoteNode(ctx context.Context, bucket, key, addr string) error {
	versions, _, err := hm.store.ListObjectVersions(ctx, bucket, key, "", "", "", 1000)
	if err != nil {
		return err
	}

	// Replicate oldest to newest to preserve version chain
	for i := len(versions) - 1; i >= 0; i-- {
		ver := versions[i]
		if ver.IsDeleteMarker {
			if err := hm.replicateDeleteMarkerToRemoteNode(ctx, bucket, key, ver.VersionID, addr); err != nil {
				return err
			}
		} else {
			if err := hm.replicateObjectVersionToRemoteNode(ctx, bucket, key, ver, addr); err != nil {
				return err
			}
		}
	}
	return nil
}

func (hm *HealingManager) replicateObjectVersionToRemoteNode(ctx context.Context, bucket, key string, ver *storage.ObjectVersion, addr string) error {
	reader, _, err := hm.store.GetObject(ctx, bucket, key, ver.VersionID)
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
	req.ContentLength = ver.Size
	req.Header.Set("Content-Type", ver.ContentType)
	req.Header.Set("X-ServStore-Replicated", "true")
	req.Header.Set("X-ServStore-Version-Id", ver.VersionID)

	req.SetBasicAuth(hm.accessKey, hm.secretKey)

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

func (hm *HealingManager) replicateDeleteMarkerToRemoteNode(ctx context.Context, bucket, key, versionID string, addr string) error {
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

	req.SetBasicAuth(hm.accessKey, hm.secretKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remote node delete marker status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (hm *HealingManager) checkRemoteKeyExists(ctx context.Context, bucket, key, addr string) (bool, error) {
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	targetURL := fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(addr, "/"), bucket, key)
	req, err := http.NewRequestWithContext(ctx, "HEAD", targetURL, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("X-ServStore-Replicated", "true")

	req.SetBasicAuth(hm.accessKey, hm.secretKey)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	return false, fmt.Errorf("status %d", resp.StatusCode)
}

func (hm *HealingManager) purgeLocalKey(ctx context.Context, bucket, key string) error {
	versions, _, err := hm.store.ListObjectVersions(ctx, bucket, key, "", "", "", 1000)
	if err != nil {
		return err
	}

	for _, ver := range versions {
		_, err := hm.store.DeleteObject(ctx, bucket, key, ver.VersionID)
		if err != nil && !errors.Is(err, storage.ErrObjectNotFound) {
			return err
		}
	}
	return nil
}
