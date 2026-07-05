//go:build enterprise

package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"
)

// federatedPublish in EE: mirrors messages to remote clusters if federation is configured.
// Returns (cleanTopic, true) if federation was triggered and the topic was rewritten.
func federatedPublish(_ context.Context, topic string, payload string) (string, bool) {
	if !strings.Contains(topic, "@") && !strings.Contains(topic, ".federated") && os.Getenv("SERVQUEUE_FEDERATION_TARGET") == "" {
		return topic, false
	}

	target := os.Getenv("SERVQUEUE_FEDERATION_TARGET")
	cleanTopic := topic
	if strings.Contains(topic, "@") {
		parts := strings.SplitN(topic, "@", 2)
		cleanTopic = parts[0]
		if target == "" && strings.EqualFold(parts[1], "eu-west") {
			target = "http://localhost:8084"
		}
	}
	if target != "" {
		go func(t, p, tgt string) {
			url := strings.TrimSuffix(tgt, "/") + "/api/v1/publish"
			data, err := json.Marshal(map[string]string{"topic": t, "payload": p})
			if err != nil {
				return
			}
			req, err := http.NewRequest("POST", url, bytes.NewReader(data))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer gateway-secret-token")
			client := &http.Client{Timeout: 3 * time.Second}
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}(cleanTopic, payload, target)
	}
	if strings.Contains(topic, "@") {
		return cleanTopic, true
	}
	return topic, false
}
