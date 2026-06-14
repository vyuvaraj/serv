package cluster

import (
	"errors"
	"hash/fnv"
	"sort"
	"strconv"
	"sync"
)

type HashRing struct {
	mu       sync.RWMutex
	replicas int
	keys     []uint32
	ring     map[uint32]string // virtualNodeHash -> physicalNodeID
	nodes    map[string]bool   // set of unique physical nodes
}

func NewHashRing(replicas int) *HashRing {
	if replicas <= 0 {
		replicas = 50
	}
	return &HashRing{
		replicas: replicas,
		ring:     make(map[uint32]string),
		nodes:    make(map[string]bool),
	}
}

func (hr *HashRing) hashKey(key string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	return h.Sum32()
}

func (hr *HashRing) AddNode(nodeID string) {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	if hr.nodes[nodeID] {
		return // Node already exists
	}
	hr.nodes[nodeID] = true

	for i := 0; i < hr.replicas; i++ {
		// e.g. "node-1#0", "node-1#1", etc.
		vNodeKey := nodeID + "#" + strconv.Itoa(i)
		hash := hr.hashKey(vNodeKey)
		hr.ring[hash] = nodeID
		hr.keys = append(hr.keys, hash)
	}

	sort.Slice(hr.keys, func(i, j int) bool {
		return hr.keys[i] < hr.keys[j]
	})
}

func (hr *HashRing) RemoveNode(nodeID string) {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	if !hr.nodes[nodeID] {
		return
	}
	delete(hr.nodes, nodeID)

	// Rebuild keys list and ring mapping
	var newKeys []uint32
	newRing := make(map[uint32]string)

	for hash, physicalID := range hr.ring {
		if physicalID != nodeID {
			newRing[hash] = physicalID
			newKeys = append(newKeys, hash)
		}
	}

	sort.Slice(newKeys, func(i, j int) bool {
		return newKeys[i] < newKeys[j]
	})

	hr.ring = newRing
	hr.keys = newKeys
}

// GetNodes returns the top 'count' unique nodes responsible for the given key.
func (hr *HashRing) GetNodes(key string, count int) ([]string, error) {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	if len(hr.keys) == 0 {
		return nil, errors.New("empty hash ring")
	}

	hash := hr.hashKey(key)
	idx := sort.Search(len(hr.keys), func(i int) bool {
		return hr.keys[i] >= hash
	})

	var chosen []string
	seen := make(map[string]bool)

	// Walk clockwise around the ring
	for i := 0; i < len(hr.keys) && len(chosen) < count; i++ {
		currIdx := (idx + i) % len(hr.keys)
		node := hr.ring[hr.keys[currIdx]]

		if !seen[node] {
			seen[node] = true
			chosen = append(chosen, node)
		}
	}

	return chosen, nil
}
