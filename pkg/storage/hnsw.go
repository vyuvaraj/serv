package storage

import (
	"math"
	"math/rand"
	"sort"
	"sync"
)

type HNSWNode struct {
	Key       string
	Vector    []float64
	Neighbors [][]string // Neighbors[level] is a list of node keys
}

type HNSWIndex struct {
	Nodes       map[string]*HNSWNode
	EnterNode   *HNSWNode
	MaxLevel    int
	M           int     // Max connections per node per layer (e.g. 16)
	EfConstruct int     // Construction search depth (e.g. 64)
	EfSearch    int     // Query search depth (e.g. 32)
	LevelDecay  float64 // Decay factor for level generation (1 / ln(M))
	mu          sync.RWMutex
}

func NewHNSWIndex() *HNSWIndex {
	m := 16
	return &HNSWIndex{
		Nodes:       make(map[string]*HNSWNode),
		M:           m,
		EfConstruct: 64,
		EfSearch:    32,
		LevelDecay:  1.0 / math.Log(float64(m)),
	}
}

func (h *HNSWIndex) randomLevel() int {
	level := 0
	// Use standard rand.Float64()
	for rand.Float64() < h.LevelDecay && level < 16 {
		level++
	}
	return level
}

// Search finds the nearest neighbors of a query vector
func (h *HNSWIndex) Search(query []float64, limit int) []*HNSWNode {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.Nodes) == 0 || h.EnterNode == nil {
		return nil
	}

	curr := h.EnterNode
	currDist := CosineDistance(query, curr.Vector)

	// Greedy search through upper levels down to level 1
	for level := h.MaxLevel; level >= 1; level-- {
		changed := true
		for changed {
			changed = false
			if level < len(curr.Neighbors) {
				for _, neighborKey := range curr.Neighbors[level] {
					nb := h.Nodes[neighborKey]
					if nb == nil {
						continue
					}
					dist := CosineDistance(query, nb.Vector)
					if dist < currDist {
						currDist = dist
						curr = nb
						changed = true
					}
				}
			}
		}
	}

	// At level 0, do a full priority queue search (beam search) with capacity efSearch
	visited := make(map[string]bool)
	visited[curr.Key] = true

	type distNode struct {
		node *HNSWNode
		dist float64
	}

	candidates := []distNode{{node: curr, dist: currDist}}
	results := []distNode{{node: curr, dist: currDist}}

	for len(candidates) > 0 {
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].dist < candidates[j].dist
		})
		currCandidate := candidates[0]
		candidates = candidates[1:]

		if len(results) >= h.EfSearch && currCandidate.dist > results[len(results)-1].dist {
			break
		}

		if len(currCandidate.node.Neighbors) > 0 {
			for _, neighborKey := range currCandidate.node.Neighbors[0] {
				if visited[neighborKey] {
					continue
				}
				visited[neighborKey] = true
				nb := h.Nodes[neighborKey]
				if nb == nil {
					continue
				}
				dist := CosineDistance(query, nb.Vector)

				if len(results) < h.EfSearch || dist < results[len(results)-1].dist {
					candidates = append(candidates, distNode{node: nb, dist: dist})
					results = append(results, distNode{node: nb, dist: dist})

					sort.Slice(results, func(i, j int) bool {
						return results[i].dist < results[j].dist
					})
					if len(results) > h.EfSearch {
						results = results[:h.EfSearch]
					}
				}
			}
		}
	}

	var finalNodes []*HNSWNode
	for i := 0; i < len(results) && i < limit; i++ {
		finalNodes = append(finalNodes, results[i].node)
	}
	return finalNodes
}

func (h *HNSWIndex) Insert(key string, vector []float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// If node already exists, update its vector and neighbors
	if _, exists := h.Nodes[key]; exists {
		h.deleteNodeNoLock(key)
	}

	newNode := &HNSWNode{
		Key:    key,
		Vector: vector,
	}
	h.Nodes[key] = newNode

	if h.EnterNode == nil {
		h.EnterNode = newNode
		h.MaxLevel = 0
		newNode.Neighbors = [][]string{{}}
		return
	}

	insertLevel := h.randomLevel()
	newNode.Neighbors = make([][]string, insertLevel+1)
	for l := 0; l <= insertLevel; l++ {
		newNode.Neighbors[l] = []string{}
	}

	curr := h.EnterNode
	currDist := CosineDistance(vector, curr.Vector)

	// 1. Find the entry point at the insertLevel by greedy search on upper layers
	for level := h.MaxLevel; level > insertLevel; level-- {
		changed := true
		for changed {
			changed = false
			if level < len(curr.Neighbors) {
				for _, neighborKey := range curr.Neighbors[level] {
					nb := h.Nodes[neighborKey]
					if nb == nil {
						continue
					}
					dist := CosineDistance(vector, nb.Vector)
					if dist < currDist {
						currDist = dist
						curr = nb
						changed = true
					}
				}
			}
		}
	}

	// 2. Insert at each level from insertLevel down to 0
	for level := min(insertLevel, h.MaxLevel); level >= 0; level-- {
		visited := make(map[string]bool)
		visited[curr.Key] = true

		type distNode struct {
			node *HNSWNode
			dist float64
		}

		candidates := []distNode{{node: curr, dist: currDist}}
		levelResults := []distNode{{node: curr, dist: currDist}}

		for len(candidates) > 0 {
			sort.Slice(candidates, func(i, j int) bool {
				return candidates[i].dist < candidates[j].dist
			})
			currCandidate := candidates[0]
			candidates = candidates[1:]

			if len(levelResults) >= h.EfConstruct && currCandidate.dist > levelResults[len(levelResults)-1].dist {
				break
			}

			if level < len(currCandidate.node.Neighbors) {
				for _, neighborKey := range currCandidate.node.Neighbors[level] {
					if visited[neighborKey] {
						continue
					}
					visited[neighborKey] = true
					nb := h.Nodes[neighborKey]
					if nb == nil {
						continue
					}
					dist := CosineDistance(vector, nb.Vector)

					if len(levelResults) < h.EfConstruct || dist < levelResults[len(levelResults)-1].dist {
						candidates = append(candidates, distNode{node: nb, dist: dist})
						levelResults = append(levelResults, distNode{node: nb, dist: dist})

						sort.Slice(levelResults, func(i, j int) bool {
							return levelResults[i].dist < levelResults[j].dist
						})
						if len(levelResults) > h.EfConstruct {
							levelResults = levelResults[:h.EfConstruct]
						}
					}
				}
			}
		}

		// Connect newNode to the nearest levelResults
		for _, r := range levelResults {
			if len(newNode.Neighbors[level]) >= h.M {
				break
			}
			newNode.Neighbors[level] = append(newNode.Neighbors[level], r.node.Key)

			if level < len(r.node.Neighbors) {
				r.node.Neighbors[level] = append(r.node.Neighbors[level], key)
				if len(r.node.Neighbors[level]) > h.M {
					h.pruneNeighborsNoLock(r.node, level)
				}
			}
		}

		if len(levelResults) > 0 {
			curr = levelResults[0].node
			currDist = levelResults[0].dist
		}
	}

	// 3. If insertLevel is higher than MaxLevel, raise MaxLevel and update entry node
	if insertLevel > h.MaxLevel {
		for l := h.MaxLevel + 1; l <= insertLevel; l++ {
			newNode.Neighbors[l] = []string{}
		}
		h.MaxLevel = insertLevel
		h.EnterNode = newNode
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (h *HNSWIndex) pruneNeighborsNoLock(node *HNSWNode, level int) {
	type distKey struct {
		key  string
		dist float64
	}
	var list []distKey
	for _, neighborKey := range node.Neighbors[level] {
		nb := h.Nodes[neighborKey]
		if nb == nil {
			continue
		}
		list = append(list, distKey{key: neighborKey, dist: CosineDistance(node.Vector, nb.Vector)})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].dist < list[j].dist
	})
	node.Neighbors[level] = nil
	for i := 0; i < len(list) && i < h.M; i++ {
		node.Neighbors[level] = append(node.Neighbors[level], list[i].key)
	}
}

func (h *HNSWIndex) deleteNodeNoLock(key string) {
	node := h.Nodes[key]
	if node == nil {
		return
	}
	for level, neighbors := range node.Neighbors {
		for _, neighborKey := range neighbors {
			nb := h.Nodes[neighborKey]
			if nb == nil || level >= len(nb.Neighbors) {
				continue
			}
			var updated []string
			for _, k := range nb.Neighbors[level] {
				if k != key {
					updated = append(updated, k)
				}
			}
			nb.Neighbors[level] = updated
		}
	}
	delete(h.Nodes, key)
	if h.EnterNode != nil && h.EnterNode.Key == key {
		h.EnterNode = nil
		h.MaxLevel = 0
		for _, anyNode := range h.Nodes {
			h.EnterNode = anyNode
			h.MaxLevel = len(anyNode.Neighbors) - 1
			break
		}
	}
}
