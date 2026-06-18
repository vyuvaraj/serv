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

func SearchIndex(id string, data interface{}) interface{} {
	searchMu.Lock()
	conn := searchConnString
	searchMu.Unlock()

	if conn == "" {
		return [2]interface{}{nil, "search not initialized; declare search \"connection_string\" first"}
	}

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

	searchMu.Lock()
	searchIndexMap[id] = data
	searchMu.Unlock()

	LogInfo("Indexed document in search: ", id)
	return true
}

func SearchQuery(q string, options interface{}) interface{} {
	searchMu.Lock()
	conn := searchConnString
	searchMu.Unlock()

	if conn == "" {
		return [2]interface{}{nil, "search not initialized; declare search \"connection_string\" first"}
	}

	// If Meilisearch REST connection
	if strings.HasPrefix(conn, "meilisearch://") {
		host := strings.TrimPrefix(conn, "meilisearch://")
		url := fmt.Sprintf("http://%s/indexes/default/search", host)
		searchParams := map[string]interface{}{"q": q}
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

	// In-memory text matching fallback for testing
	searchMu.RLock()
	defer searchMu.RUnlock()

	var hits []interface{}
	queryLower := strings.ToLower(q)

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
