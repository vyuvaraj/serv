package provision

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"servconsole/pkg/config"
)

var (
	CustomBuckets   = make([]string, 0)
	CustomBucketsMu sync.Mutex

	CustomTopics   = make([]string, 0)
	CustomTopicsMu sync.Mutex

	WriteJSONError func(http.ResponseWriter, *http.Request, string, string, int)
	AddAuditLog    func(user string, action string, method string, path string, status int)
)

func Init(
	writeError func(http.ResponseWriter, *http.Request, string, string, int),
	auditLog func(string, string, string, string, int),
) {
	WriteJSONError = writeError
	AddAuditLog = auditLog
}

func HandleProvisionStore(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		CustomBucketsMu.Lock()
		defer CustomBucketsMu.Unlock()
		json.NewEncoder(w).Encode(CustomBuckets)
		return
	}

	if r.Method != http.MethodPost {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		BucketName string `json:"bucketName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.BucketName == "" {
		WriteJSONError(w, r, "Invalid request body", "ERR_INVALID_BODY", http.StatusBadRequest)
		return
	}

	bucketName := strings.ToLower(strings.TrimSpace(req.BucketName))

	client := http.Client{Timeout: 1 * time.Second}
	putUrl := fmt.Sprintf("%s/%s", strings.TrimSuffix(config.ActiveDiscovery.Store, "/"), bucketName)
	realReq, _ := http.NewRequest(http.MethodPut, putUrl, nil)
	realResp, err := client.Do(realReq)

	realSuccess := false
	if err == nil {
		realResp.Body.Close()
		if realResp.StatusCode == http.StatusOK || realResp.StatusCode == http.StatusCreated {
			realSuccess = true
		}
	}

	CustomBucketsMu.Lock()
	found := false
	for _, b := range CustomBuckets {
		if b == bucketName {
			found = true
			break
		}
	}
	if !found {
		CustomBuckets = append(CustomBuckets, bucketName)
	}
	CustomBucketsMu.Unlock()

	if AddAuditLog != nil {
		AddAuditLog("console-operator", "Create Bucket: "+bucketName, r.Method, r.URL.Path, http.StatusOK)
	}

	json.NewEncoder(w).Encode(map[string]any{
		"success":     true,
		"bucketName":  bucketName,
		"realGateway": realSuccess,
		"message":     fmt.Sprintf("Bucket '%s' successfully provisioned.", bucketName),
	})
}

func HandleProvisionQueue(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		CustomTopicsMu.Lock()
		defer CustomTopicsMu.Unlock()
		json.NewEncoder(w).Encode(CustomTopics)
		return
	}

	if r.Method != http.MethodPost {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TopicName string `json:"topicName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TopicName == "" {
		WriteJSONError(w, r, "Invalid request body", "ERR_INVALID_BODY", http.StatusBadRequest)
		return
	}

	topicName := strings.ToLower(strings.TrimSpace(req.TopicName))

	client := http.Client{Timeout: 1 * time.Second}
	postUrl := fmt.Sprintf("%s/api/topics/%s/schema", strings.TrimSuffix(config.ActiveDiscovery.Queue, "/"), topicName)
	realReq, _ := http.NewRequest(http.MethodPost, postUrl, strings.NewReader("{}"))
	realReq.Header.Set("Authorization", "Bearer secret-token")
	realReq.Header.Set("Content-Type", "application/json")
	realResp, err := client.Do(realReq)

	realSuccess := false
	if err == nil {
		realResp.Body.Close()
		if realResp.StatusCode == http.StatusOK {
			realSuccess = true
		}
	}

	CustomTopicsMu.Lock()
	found := false
	for _, t := range CustomTopics {
		if t == topicName {
			found = true
			break
		}
	}
	if !found {
		CustomTopics = append(CustomTopics, topicName)
	}
	CustomTopicsMu.Unlock()

	if AddAuditLog != nil {
		AddAuditLog("console-operator", "Create Topic: "+topicName, r.Method, r.URL.Path, http.StatusOK)
	}

	json.NewEncoder(w).Encode(map[string]any{
		"success":     true,
		"topicName":   topicName,
		"realGateway": realSuccess,
		"message":     fmt.Sprintf("Topic '%s' successfully provisioned.", topicName),
	})
}
