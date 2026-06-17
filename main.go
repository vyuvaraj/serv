package main

import (
	"log"
	"os"

	"servqueue/pkg/broker"
	"servqueue/pkg/otel"
	"servqueue/pkg/stomp"
	"servqueue/pkg/web"
)

func main() {
	// Initialize OpenTelemetry
	otel.Init()

	engine := broker.NewBrokerEngine()

	// Read credentials and TLS certificate config from environment or default
	stompUser := "admin"
	stompPass := "secret"
	apiToken := "secret-token"

	// TLS config from environment
	tlsCert := os.Getenv("TLS_CERT_FILE")
	tlsKey := os.Getenv("TLS_KEY_FILE")
	
	stompServer := stomp.NewServer(":61613", engine, stompUser, stompPass, tlsCert, tlsKey)
	webServer := web.NewServer(":8082", engine, apiToken, tlsCert, tlsKey)

	log.Println("Starting ServQueue STOMP server on tcp://:61613...")
	go func() {
		if err := stompServer.Start(); err != nil {
			log.Fatalf("STOMP server error: %v", err)
		}
	}()

	log.Println("Starting ServQueue HTTP management server on http://:8082...")
	if err := webServer.Start(); err != nil {
		log.Fatalf("HTTP server error: %v", err)
	}
}
