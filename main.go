package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"text/template"

	"github.com/vyuvaraj/ServShared"
)

type SendRequest struct {
	Channel  string                 `json:"channel"`  // email, slack, sms
	Target   string                 `json:"target"`   // email address, webhook URL, or phone number
	Template string                 `json:"template"` // Go template text
	Context  map[string]interface{} `json:"context"`  // template variables
}

type SendResponse struct {
	Status      string `json:"status"`
	DeliveredTo string `json:"delivered_to"`
	Body        string `json:"body"`
}

func main() {
	portStr := flag.String("port", "8094", "ServMail server port")
	flag.Parse()

	port := os.Getenv("PORT")
	if port == "" {
		port = *portStr
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/api/mail/send", handleSend)

	serverHandler := ServShared.AuthMiddleware(mux)

	log.Printf("ServMail server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, serverHandler); err != nil {
		log.Fatalf("failed to start ServMail: %v", err)
	}
}

func handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Channel == "" || req.Target == "" || req.Template == "" {
		http.Error(w, "Channel, target, and template are required", http.StatusBadRequest)
		return
	}

	// 1. Render template
	tmpl, err := template.New("notification").Parse(req.Template)
	if err != nil {
		http.Error(w, "Template compile error: "+err.Error(), http.StatusBadRequest)
		return
	}

	var renderedBody bytes.Buffer
	if err := tmpl.Execute(&renderedBody, req.Context); err != nil {
		http.Error(w, "Template execution error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	bodyStr := renderedBody.String()

	// 2. Deliver via channel (simulate outputs)
	channelLower := strings.ToLower(req.Channel)
	switch channelLower {
	case "email":
		log.Printf("[ServMail] [EMAIL] Sending to %s: %s", req.Target, bodyStr)
	case "slack":
		log.Printf("[ServMail] [SLACK] Posting to webhook %s: %s", req.Target, bodyStr)
	case "sms":
		log.Printf("[ServMail] [SMS] Sending to number %s: %s", req.Target, bodyStr)
	default:
		http.Error(w, "Unsupported delivery channel: "+req.Channel, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(SendResponse{
		Status:      "delivered",
		DeliveredTo: req.Target,
		Body:        bodyStr,
	})
}
