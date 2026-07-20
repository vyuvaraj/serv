package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// runQueueTail streams recent messages from a ServQueue topic to stdout.
// Usage: serv queue tail <topic> [--host <url>] [--limit <n>]
func runQueueTail() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: serv queue tail <topic> [--host <url>] [--limit <n>]")
		fmt.Println("       serv queue tail <topic> --host http://localhost:8085")
		os.Exit(1)
	}

	topic := os.Args[3]
	host := "http://localhost:8085"
	limit := "20"

	if envHost := os.Getenv("SERV_QUEUE_URL"); envHost != "" {
		host = envHost
	}

	for i := 4; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--host", "-host":
			if i+1 < len(os.Args) {
				host = os.Args[i+1]
				i++
			}
		case "--limit", "-limit", "-n":
			if i+1 < len(os.Args) {
				limit = os.Args[i+1]
				i++
			}
		}
	}

	url := fmt.Sprintf("%s/api/messages/%s?limit=%s", strings.TrimSuffix(host, "/"), topic, limit)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Printf("Failed to create request: %v\n", err)
		os.Exit(1)
	}
	if token := os.Getenv("SERV_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error connecting to ServQueue at %s: %v\n", host, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("ServQueue returned %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var messages []map[string]interface{}
	if err := json.Unmarshal(body, &messages); err != nil {
		// Not a JSON array — print raw (SSE or plain text tail)
		fmt.Println(string(body))
		return
	}

	if len(messages) == 0 {
		fmt.Printf("No messages on topic %q\n", topic)
		return
	}

	fmt.Printf("Topic: %s  (%d messages)\n", topic, len(messages))
	fmt.Println(strings.Repeat("─", 60))
	for i, msg := range messages {
		ts, _ := msg["timestamp"].(string)
		payload, _ := json.Marshal(msg["payload"])
		fmt.Printf("[%d] %s\n    %s\n\n", i+1, ts, string(payload))
	}
}

// runQueueList lists all topics registered in ServQueue.
// Usage: serv queue list [--host <url>]
func runQueueList() {
	host := "http://localhost:8085"
	if envHost := os.Getenv("SERV_QUEUE_URL"); envHost != "" {
		host = envHost
	}
	for i := 3; i < len(os.Args); i++ {
		if (os.Args[i] == "--host" || os.Args[i] == "-host") && i+1 < len(os.Args) {
			host = os.Args[i+1]
			i++
		}
	}

	url := fmt.Sprintf("%s/api/topics", strings.TrimSuffix(host, "/"))
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Printf("Failed to create request: %v\n", err)
		os.Exit(1)
	}
	if token := os.Getenv("SERV_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error connecting to ServQueue at %s: %v\n", host, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("ServQueue returned %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var topics []map[string]interface{}
	if err := json.Unmarshal(body, &topics); err != nil {
		fmt.Println(string(body))
		return
	}

	if len(topics) == 0 {
		fmt.Println("No topics registered.")
		return
	}

	fmt.Printf("%-30s  %-10s  %s\n", "TOPIC", "CONSUMERS", "MESSAGES")
	fmt.Println(strings.Repeat("─", 60))
	for _, t := range topics {
		name, _ := t["name"].(string)
		consumers := fmt.Sprintf("%v", t["consumers"])
		messages := fmt.Sprintf("%v", t["message_count"])
		fmt.Printf("%-30s  %-10s  %s\n", name, consumers, messages)
	}
}

// runQueueDLQ inspects a dead letter queue topic or the registered DLQ of a topic.
// Usage: serv queue dlq inspect <topic> [--host <url>] [--replay]
func runQueueDLQ() {
	if len(os.Args) < 5 || os.Args[3] != "inspect" {
		fmt.Println("Usage: serv queue dlq inspect <topic> [--host <url>] [--replay]")
		os.Exit(1)
	}

	topic := os.Args[4]
	host := "http://localhost:8085"
	replay := false

	if envHost := os.Getenv("SERV_QUEUE_URL"); envHost != "" {
		host = envHost
	}

	for i := 5; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--host", "-host":
			if i+1 < len(os.Args) {
				host = os.Args[i+1]
				i++
			}
		case "--replay", "-replay":
			replay = true
		}
	}

	// Fetch DLQ messages from GET endpoint: /api/v1/topics/{topic}/dlq
	url := fmt.Sprintf("%s/api/v1/topics/%s/dlq", strings.TrimSuffix(host, "/"), topic)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Printf("Failed to create request: %v\n", err)
		os.Exit(1)
	}
	if token := os.Getenv("SERV_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error connecting to ServQueue at %s: %v\n", host, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("ServQueue returned %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	type DLQMessage struct {
		MessageID       string `json:"message_id"`
		SourceTopic     string `json:"source_topic"`
		OriginalPayload string `json:"original_payload"`
		FailureReason   string `json:"failure_reason"`
		Timestamp       int64  `json:"timestamp"`
		RetryCount      int    `json:"retry_count"`
	}

	var messages []DLQMessage
	if err := json.Unmarshal(body, &messages); err != nil {
		fmt.Printf("Failed to parse response: %v\n", err)
		os.Exit(1)
	}

	if len(messages) == 0 {
		fmt.Printf("No DLQ messages found for topic %q\n", topic)
		return
	}

	if replay {
		fmt.Printf("Replaying %d DLQ messages back to their source topics...\n", len(messages))
		for _, msg := range messages {
			// Publish back to source topic
			pubURL := fmt.Sprintf("%s/api/v1/publish", strings.TrimSuffix(host, "/"))
			pubBody := fmt.Sprintf(`{"topic":%q,"payload":%q,"message_id":%q}`, msg.SourceTopic, msg.OriginalPayload, msg.MessageID)
			pubReq, err := http.NewRequest("POST", pubURL, strings.NewReader(pubBody))
			if err != nil {
				fmt.Printf("Failed to create publish request for message %s: %v\n", msg.MessageID, err)
				continue
			}
			pubReq.Header.Set("Content-Type", "application/json")
			if token := os.Getenv("SERV_TOKEN"); token != "" {
				pubReq.Header.Set("Authorization", "Bearer "+token)
			}
			pubResp, err := client.Do(pubReq)
			if err != nil {
				fmt.Printf("Failed to replay message %s: %v\n", msg.MessageID, err)
				continue
			}
			pubResp.Body.Close()
			if pubResp.StatusCode == http.StatusOK {
				fmt.Printf("Successfully replayed message %s to topic %q\n", msg.MessageID, msg.SourceTopic)
			} else {
				fmt.Printf("Failed to replay message %s: status %d\n", msg.MessageID, pubResp.StatusCode)
			}
		}
		return
	}

	fmt.Printf("DLQ Inspection for Topic: %s  (%d messages)\n", topic, len(messages))
	fmt.Println(strings.Repeat("─", 80))
	fmt.Printf("%-24s  %-20s  %-20s  %-6s  %s\n", "MESSAGE ID", "SOURCE TOPIC", "REASON", "RETRY", "PAYLOAD")
	fmt.Println(strings.Repeat("─", 80))
	for _, msg := range messages {
		pld := msg.OriginalPayload
		if len(pld) > 30 {
			pld = pld[:27] + "..."
		}
		fmt.Printf("%-24s  %-20s  %-20s  %-6d  %s\n", msg.MessageID, msg.SourceTopic, msg.FailureReason, msg.RetryCount, pld)
	}
}
