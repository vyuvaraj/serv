package main

import (
	"log"

	"servqueue/pkg/broker"
	"servqueue/pkg/stomp"
	"servqueue/pkg/web"
)

func main() {
	engine := broker.NewBrokerEngine()

	stompServer := stomp.NewServer(":61613", engine)
	webServer := web.NewServer(":8082", engine)

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
