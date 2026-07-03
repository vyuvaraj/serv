package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
)

func runCacheInspect() {
	host := "http://localhost:8086"
	if envHost := os.Getenv("SERV_CACHE_URL"); envHost != "" {
		host = envHost
	}

	for i := 2; i < len(os.Args); i++ {
		if (os.Args[i] == "--host" || os.Args[i] == "-host") && i+1 < len(os.Args) {
			host = os.Args[i+1]
			i++
		}
	}

	url := fmt.Sprintf("%s/api/cache/inspect", strings.TrimSuffix(host, "/"))
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Printf("Failed to create request: %v\n", err)
		os.Exit(1)
	}

	authToken := os.Getenv("SERV_CACHE_AUTH_TOKEN")
	if authToken == "" {
		authToken = "secret-token" // default
	}
	req.Header.Set("Authorization", "Bearer "+authToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Failed to connect to ServCache: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("ServCache returned error status %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var data struct {
		TotalKeys  int            `json:"total_keys"`
		Namespaces map[string]int `json:"namespaces"`
		Hits       uint64         `json:"hits"`
		Misses     uint64         `json:"misses"`
		HitRatio   float64        `json:"hit_ratio"`
		HotKeys    map[string]int `json:"hot_keys"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		fmt.Printf("Failed to parse response: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== ServCache Telemetry Inspector ===")
	fmt.Printf("Total Cached Keys: %d\n", data.TotalKeys)
	fmt.Printf("Total Hits:        %d\n", data.Hits)
	fmt.Printf("Total Misses:      %d\n", data.Misses)
	fmt.Printf("Hit/Miss Ratio:    %.2f%%\n", data.HitRatio*100)
	fmt.Println("\n--- Namespaces Key Count ---")
	if len(data.Namespaces) == 0 {
		fmt.Println("  (No namespaces defined)")
	} else {
		for ns, count := range data.Namespaces {
			fmt.Printf("  Namespace [%s]: %d keys\n", ns, count)
		}
	}

	fmt.Println("\n--- Top Hot Keys ---")
	if len(data.HotKeys) == 0 {
		fmt.Println("  (No active key requests tracked)")
	} else {
		type keyAccess struct {
			key   string
			count int
		}
		var sorted []keyAccess
		for k, v := range data.HotKeys {
			sorted = append(sorted, keyAccess{k, v})
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].count > sorted[j].count
		})

		limit := 5
		if len(sorted) < limit {
			limit = len(sorted)
		}
		for i := 0; i < limit; i++ {
			fmt.Printf("  %d. %s -> %d reads\n", i+1, sorted[i].key, sorted[i].count)
		}
	}
}
