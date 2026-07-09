package edge

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

type WASMHandler func(payload string) (string, error)

type EdgeNode struct {
	Region   string
	Handlers map[string]WASMHandler
	DataStore map[string][]byte
}

type SyncRecord struct {
	Key       string
	Value     []byte
	Timestamp time.Time
}

type EdgeRuntime struct {
	mu        sync.RWMutex
	nodes     map[string]*EdgeNode
	syncQueue []SyncRecord
	primaryDB map[string][]byte
}

func NewEdgeRuntime() *EdgeRuntime {
	rt := &EdgeRuntime{
		nodes:     make(map[string]*EdgeNode),
		primaryDB: make(map[string][]byte),
	}
	// Pre-populate standard edge regions
	for _, region := range []string{"us-east", "eu-west", "ap-south"} {
		rt.nodes[region] = &EdgeNode{
			Region:    region,
			Handlers:  make(map[string]WASMHandler),
			DataStore: make(map[string][]byte),
		}
	}
	return rt
}

// RouteGeo selects the closest edge node based on X-Client-Geo header or geolocation metadata
func (rt *EdgeRuntime) RouteGeo(geoHeader string) (*EdgeNode, error) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	// Default to us-east if unspecified or unrecognized
	region := "us-east"
	switch geoHeader {
	case "US", "North-America":
		region = "us-east"
	case "EU", "Europe":
		region = "eu-west"
	case "IN", "AS", "Asia":
		region = "ap-south"
	}

	node, ok := rt.nodes[region]
	if !ok {
		return nil, fmt.Errorf("region node %s not found", region)
	}
	return node, nil
}

// RegisterWASMHandler deploys a function to all edge nodes
func (rt *EdgeRuntime) RegisterWASMHandler(name string, handler WASMHandler) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	for _, node := range rt.nodes {
		node.Handlers[name] = handler
	}
}

// WriteEdge writes local data to an edge node and buffers a sync record
func (rt *EdgeRuntime) WriteEdge(region, key string, val []byte) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	node, ok := rt.nodes[region]
	if !ok {
		return fmt.Errorf("invalid region: %s", region)
	}

	node.DataStore[key] = val

	// Buffer update for offline sync replication
	rt.syncQueue = append(rt.syncQueue, SyncRecord{
		Key:       key,
		Value:     val,
		Timestamp: time.Now(),
	})
	return nil
}

// FlushSyncQueues replications edge local data to primary DB node
func (rt *EdgeRuntime) FlushSyncQueues() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	flushed := len(rt.syncQueue)
	for _, rec := range rt.syncQueue {
		rt.primaryDB[rec.Key] = rec.Value
	}
	rt.syncQueue = nil
	return flushed
}

// ReadPrimary retrieves synced primary database data
func (rt *EdgeRuntime) ReadPrimary(key string) ([]byte, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	val, ok := rt.primaryDB[key]
	return val, ok
}

// ExecuteWASM runs the compiled function on the chosen region node
func (rt *EdgeRuntime) ExecuteWASM(region, funcName, payload string) (string, error) {
	rt.mu.RLock()
	node, ok := rt.nodes[region]
	rt.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("region not found: %s", region)
	}

	handler, ok := node.Handlers[funcName]
	if !ok {
		return "", errors.New("WASM function not deployed")
	}

	return handler(payload)
}
