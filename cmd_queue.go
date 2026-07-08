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
