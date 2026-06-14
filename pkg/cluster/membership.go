package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

type NodeInfo struct {
	NodeID   string    `json:"node_id"`
	Address  string    `json:"address"`
	Status   string    `json:"status"` // "online" or "offline"
	LastSeen time.Time `json:"last_seen"`
	Load     int64     `json:"load"`
	Region   string    `json:"region"`
}

type MembershipManager struct {
	mu        sync.RWMutex
	localNode *NodeInfo
	peers     map[string]*NodeInfo // NodeID -> NodeInfo
	client    *http.Client
	stopChan  chan struct{}
	active    bool
	ring      *HashRing
}

type GossipPayload struct {
	SourceNode NodeInfo             `json:"source_node"`
	Peers      map[string]*NodeInfo `json:"peers"`
}

func NewMembershipManager(nodeID, address string, bootstrapPeers string) *MembershipManager {
	local := &NodeInfo{
		NodeID:   nodeID,
		Address:  address,
		Status:   "online",
		LastSeen: time.Now(),
	}

	mm := &MembershipManager{
		localNode: local,
		peers:     make(map[string]*NodeInfo),
		client:    &http.Client{Timeout: 2 * time.Second},
		stopChan:  make(chan struct{}),
		ring:      NewHashRing(50),
	}

	// Add local node to ring initially
	mm.ring.AddNode(local.NodeID)

	// Load bootstrap peers
	if bootstrapPeers != "" {
		for _, addr := range strings.Split(bootstrapPeers, ",") {
			addr = strings.TrimSpace(addr)
			if addr != "" && addr != address {
				// Initialize bootstrap peer with address as placeholder key
				mm.peers[addr] = &NodeInfo{
					NodeID:   addr,
					Address:  addr,
					Status:   "offline",
					LastSeen: time.Time{},
				}
			}
		}
	}

	return mm
}

func (mm *MembershipManager) WithRegion(region string) *MembershipManager {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.localNode.Region = region
	return mm
}

func (mm *MembershipManager) Ring() *HashRing {
	return mm.ring
}

func (mm *MembershipManager) Start(ctx context.Context) {
	mm.mu.Lock()
	if mm.active {
		mm.mu.Unlock()
		return
	}
	mm.active = true
	mm.mu.Unlock()

	slog.Info("Starting cluster membership manager...", "node_id", mm.localNode.NodeID, "addr", mm.localNode.Address)

	go mm.heartbeatLoop()
}

func (mm *MembershipManager) Stop() {
	mm.mu.Lock()
	if !mm.active {
		mm.mu.Unlock()
		return
	}
	mm.active = false
	close(mm.stopChan)
	mm.mu.Unlock()
}

func (mm *MembershipManager) GetNodes() []NodeInfo {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	nodes := []NodeInfo{*mm.localNode}
	for _, peer := range mm.peers {
		nodes = append(nodes, *peer)
	}
	return nodes
}

func (mm *MembershipManager) GetNodeAddress(nodeID string) (string, bool) {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	if mm.localNode.NodeID == nodeID {
		return mm.localNode.Address, true
	}
	if peer, exists := mm.peers[nodeID]; exists {
		return peer.Address, true
	}
	return "", false
}

func (mm *MembershipManager) LocalNodeID() string {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return mm.localNode.NodeID
}

func (mm *MembershipManager) IsNodeOnline(nodeID string) bool {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	if mm.localNode.NodeID == nodeID {
		return mm.localNode.Status == "online"
	}
	if peer, exists := mm.peers[nodeID]; exists {
		return peer.Status == "online"
	}
	return false
}



// MergeGossip processes incoming gossip payload and returns local state to sender.
func (mm *MembershipManager) MergeGossip(payload GossipPayload) GossipPayload {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	// Update or add the sending node
	sender := payload.SourceNode
	if sender.NodeID != mm.localNode.NodeID {
		mm.updatePeerInfo(&sender)
	}

	// Merge all reported peers
	for _, peer := range payload.Peers {
		if peer.NodeID == mm.localNode.NodeID {
			continue // Skip info about ourselves
		}
		mm.updatePeerInfo(peer)
	}

	// Build return payload
	peersCopy := make(map[string]*NodeInfo)
	for k, v := range mm.peers {
		peersCopy[k] = v
	}

	return GossipPayload{
		SourceNode: *mm.localNode,
		Peers:      peersCopy,
	}
}

func (mm *MembershipManager) updatePeerInfo(peer *NodeInfo) {
	// If bootstrap placeholder matches, clean it up
	if _, isPlaceholder := mm.peers[peer.Address]; isPlaceholder && peer.Address != peer.NodeID {
		delete(mm.peers, peer.Address)
	}

	existing, exists := mm.peers[peer.NodeID]
	if !exists {
		// New node discovered
		peerCopy := *peer
		if peerCopy.Status == "online" && peerCopy.LastSeen.IsZero() {
			peerCopy.LastSeen = time.Now()
		}
		mm.peers[peer.NodeID] = &peerCopy
		// ONLY add to hash ring if they belong to our region!
		if peerCopy.Status == "online" && (peerCopy.Region == "" || peerCopy.Region == mm.localNode.Region) {
			mm.ring.AddNode(peer.NodeID)
		}
		slog.Info("Discovered new cluster node", "node_id", peer.NodeID, "address", peer.Address, "region", peer.Region)
		return
	}

	// If info is newer, update it
	if peer.LastSeen.After(existing.LastSeen) {
		oldStatus := existing.Status
		existing.Status = peer.Status
		existing.LastSeen = peer.LastSeen
		existing.Load = peer.Load
		existing.Address = peer.Address
		existing.Region = peer.Region

		// Rebuild ring membership based on status transition and matching region
		if oldStatus != existing.Status {
			if existing.Status == "online" && (existing.Region == "" || existing.Region == mm.localNode.Region) {
				mm.ring.AddNode(peer.NodeID)
			} else {
				mm.ring.RemoveNode(peer.NodeID)
			}
		}
	}
}

func (mm *MembershipManager) heartbeatLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-mm.stopChan:
			return
		case <-ticker.C:
			mm.sendHeartbeats()
			mm.checkTimeouts()
		}
	}
}

func (mm *MembershipManager) sendHeartbeats() {
	mm.mu.Lock()
	mm.localNode.LastSeen = time.Now()
	// Get current list of peers to ping
	var targets []*NodeInfo
	for _, peer := range mm.peers {
		targets = append(targets, peer)
	}
	mm.mu.Unlock()

	var wg sync.WaitGroup
	for _, target := range targets {
		wg.Add(1)
		go func(peer *NodeInfo) {
			defer wg.Done()
			mm.pingPeer(peer)
		}(target)
	}
	wg.Wait()
}

func (mm *MembershipManager) pingPeer(peer *NodeInfo) {
	mm.mu.RLock()
	payload := GossipPayload{
		SourceNode: *mm.localNode,
		Peers:      make(map[string]*NodeInfo),
	}
	for k, v := range mm.peers {
		payload.Peers[k] = v
	}
	mm.mu.RUnlock()

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return
	}

	scheme := "http"
	// Ensure we handle URL schemes correctly
	addr := peer.Address
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = scheme + "://" + addr
	}
	gossipURL := fmt.Sprintf("%s/console/cluster/gossip", strings.TrimSuffix(addr, "/"))

	req, err := http.NewRequest("POST", gossipURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := mm.client.Do(req)
	if err != nil {
		// Failed to contact peer
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var reply GossipPayload
		if err := json.NewDecoder(resp.Body).Decode(&reply); err == nil {
			mm.mu.Lock()
			// Update this peer as online
			peer.Status = "online"
			peer.LastSeen = time.Now()
			peer.NodeID = reply.SourceNode.NodeID

			// Merge received nodes
			mm.updatePeerInfo(&reply.SourceNode)
			for _, node := range reply.Peers {
				if node.NodeID != mm.localNode.NodeID {
					mm.updatePeerInfo(node)
				}
			}
			mm.mu.Unlock()
		}
	}
}

func (mm *MembershipManager) checkTimeouts() {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	now := time.Now()
	timeout := 10 * time.Second

	for _, peer := range mm.peers {
		if peer.Status == "online" && !peer.LastSeen.IsZero() && now.Sub(peer.LastSeen) > timeout {
			peer.Status = "offline"
			mm.ring.RemoveNode(peer.NodeID)
			slog.Warn("Node went offline (heartbeat timeout)", "node_id", peer.NodeID, "address", peer.Address)
		}
	}
}
