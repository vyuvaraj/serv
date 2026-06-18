package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

var (
	searchConnString string
	searchMu         sync.RWMutex
	searchIndexMap   = make(map[string]interface{})
)

func InitSearch(connStr string) {
	searchMu.Lock()
	defer searchMu.Unlock()
	searchConnString = connStr
	LogInfo("Search client initialized: ", connStr)
}

func SearchIndex(id interface{}, data interface{}) interface{} {
	searchMu.Lock()
	conn := searchConnString
	searchMu.Unlock()

	if conn == "" {
		return [2]interface{}{nil, "search not initialized; declare search \"connection_string\" first"}
	}

	idStr := fmt.Sprint(id)

	// If Meilisearch REST connection
	if strings.HasPrefix(conn, "meilisearch://") {
		host := strings.TrimPrefix(conn, "meilisearch://")
		url := fmt.Sprintf("http://%s/indexes/default/documents", host)
		payloadBytes, err := json.Marshal(data)
		if err == nil {
			req, err := http.NewRequest("POST", url, bytes.NewReader(payloadBytes))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
				client := &http.Client{}
				resp, err := client.Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}
		}
	}

	// Elasticsearch REST connection
	if strings.HasPrefix(conn, "elastic://") {
		hostAndIndex := strings.TrimPrefix(conn, "elastic://")
		parts := strings.SplitN(hostAndIndex, "/", 2)
		host := parts[0]
		index := "default"
		if len(parts) == 2 && parts[1] != "" {
			index = parts[1]
		}
		url := fmt.Sprintf("http://%s/%s/_doc/%s", host, index, idStr)
		payloadBytes, err := json.Marshal(data)
		if err == nil {
			req, err := http.NewRequest("PUT", url, bytes.NewReader(payloadBytes))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
				client := &http.Client{}
				resp, err := client.Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}
		}
	}

	searchMu.Lock()
	searchIndexMap[idStr] = data
	searchMu.Unlock()

	LogInfo("Indexed document in search: ", idStr)
	return true
}

func SearchQuery(q interface{}, options interface{}) interface{} {
	searchMu.Lock()
	conn := searchConnString
	searchMu.Unlock()

	if conn == "" {
		return [2]interface{}{nil, "search not initialized; declare search \"connection_string\" first"}
	}

	queryStr := fmt.Sprint(q)

	// If Meilisearch REST connection
	if strings.HasPrefix(conn, "meilisearch://") {
		host := strings.TrimPrefix(conn, "meilisearch://")
		url := fmt.Sprintf("http://%s/indexes/default/search", host)
		searchParams := map[string]interface{}{"q": queryStr}
		payloadBytes, err := json.Marshal(searchParams)
		if err == nil {
			req, err := http.NewRequest("POST", url, bytes.NewReader(payloadBytes))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
				client := &http.Client{}
				resp, err := client.Do(req)
				if err == nil {
					defer resp.Body.Close()
					var result map[string]interface{}
					if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
						if hits, ok := result["hits"]; ok {
							return hits
						}
					}
				}
			}
		}
	}

	// Elasticsearch REST connection
	if strings.HasPrefix(conn, "elastic://") {
		hostAndIndex := strings.TrimPrefix(conn, "elastic://")
		parts := strings.SplitN(hostAndIndex, "/", 2)
		host := parts[0]
		index := "default"
		if len(parts) == 2 && parts[1] != "" {
			index = parts[1]
		}
		url := fmt.Sprintf("http://%s/%s/_search", host, index)
		searchBody := map[string]interface{}{
			"query": map[string]interface{}{
				"multi_match": map[string]interface{}{
					"query": queryStr,
				},
			},
		}
		payloadBytes, err := json.Marshal(searchBody)
		if err == nil {
			req, err := http.NewRequest("POST", url, bytes.NewReader(payloadBytes))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
				client := &http.Client{}
				resp, err := client.Do(req)
				if err == nil {
					defer resp.Body.Close()
					var result map[string]interface{}
					if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
						if hitsObj, ok := result["hits"].(map[string]interface{}); ok {
							if hits, ok := hitsObj["hits"]; ok {
								return hits
							}
						}
					}
				}
			}
		}
	}

	// In-memory text matching fallback for testing
	searchMu.RLock()
	defer searchMu.RUnlock()

	var hits []interface{}
	queryLower := strings.ToLower(queryStr)

	for _, doc := range searchIndexMap {
		docBytes, err := json.Marshal(doc)
		if err == nil {
			docStr := strings.ToLower(string(docBytes))
			if strings.Contains(docStr, queryLower) {
				hits = append(hits, doc)
			}
		}
	}

	return hits
}
