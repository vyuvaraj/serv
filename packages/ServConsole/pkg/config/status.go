package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/vyuvaraj/ServShared"
)

func HandleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	statuses := []ComponentStatus{
		CheckStatus("ServGate", *GateUrl),
		CheckStatus("ServStore", *StoreUrl),
		CheckStatus("ServQueue", *QueueUrl),
		CheckStatus("ServTrace", *TraceUrl),
		CheckStatus("ServTunnel", *TunnelUrl),
		CheckStatus("ServAuth", *AuthUrl),
		CheckStatus("ServDB", *DbUrl),
		CheckStatus("ServMail", *MailUrl),
		CheckStatus("ServFlow", *FlowUrl),
		CheckStatus("ServMesh", *MeshUrl),
		CheckStatus("ServCron", *CronUrl),
		CheckStatus("ServCache", *CacheUrl),
		CheckStatus("ServRegistry", *RegistryUrl),
		CheckStatus("ServCloud", *CloudUrl),
		CheckStatus("ServDocs", *DocsUrl),
	}

	json.NewEncoder(w).Encode(map[string]any{
		"timestamp":  time.Now().Format(time.RFC3339),
		"components": statuses,
	})
}

func CheckStatus(name string, baseUrl string) ComponentStatus {
	if baseUrl == "" {
		return ComponentStatus{Name: name, Online: false, Url: baseUrl}
	}

	client := http.Client{
		Timeout: 1 * time.Second,
	}

	reqUrl := fmt.Sprintf("%s/healthz", strings.TrimSuffix(baseUrl, "/"))
	req, err := http.NewRequest("GET", reqUrl, nil)
	if err != nil {
		return ComponentStatus{Name: name, Online: false, Url: baseUrl}
	}

	if jwtSec := os.Getenv("SERV_JWT_SECRET"); jwtSec != "" {
		svcToken, _ := ServShared.GenerateServiceToken(jwtSec, "servconsole")
		if svcToken != "" {
			req.Header.Set("Authorization", "Bearer "+svcToken)
		}
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return ComponentStatus{Name: name, Online: false, Url: baseUrl}
	}
	resp.Body.Close()

	latency := time.Since(start).Milliseconds()

	if resp.StatusCode != http.StatusOK {
		return ComponentStatus{
			Name:      name,
			Online:    false,
			Url:       baseUrl,
			LatencyMs: latency,
		}
	}

	var details any
	var detailsPath string
	switch name {
	case "ServStore":
		detailsPath = "/console/metrics"
	case "ServQueue":
		detailsPath = "/api/stats"
	case "ServGate":
		detailsPath = "/"
	}

	if detailsPath != "" {
		detUrl := fmt.Sprintf("%s%s", strings.TrimSuffix(baseUrl, "/"), detailsPath)
		dreq, derr := http.NewRequest("GET", detUrl, nil)
		if derr == nil {
			if jwtSec := os.Getenv("SERV_JWT_SECRET"); jwtSec != "" {
				svcToken, _ := ServShared.GenerateServiceToken(jwtSec, "servconsole")
				if svcToken != "" {
					dreq.Header.Set("Authorization", "Bearer "+svcToken)
				}
			}
			dresp, derr2 := client.Do(dreq)
			if derr2 == nil {
				bodyBytes, _ := io.ReadAll(dresp.Body)
				dresp.Body.Close()
				if len(bodyBytes) > 0 {
					_ = json.Unmarshal(bodyBytes, &details)
				}
			}
		}
	}

	return ComponentStatus{
		Name:      name,
		Online:    true,
		Url:       baseUrl,
		LatencyMs: latency,
		Details:   details,
	}
}
