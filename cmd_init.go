package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func initProject() {
	name := "my-service"
	if len(os.Args) >= 3 {
		name = os.Args[2]
	}

	// Create project directory
	if err := os.MkdirAll(name, 0755); err != nil {
		fmt.Printf("Failed to create directory: %v\n", err)
		os.Exit(1)
	}

	// main.srv
	mainSrv := `server "8080"

// Path parameter: curl http://localhost:8080/api/hello/Alice
route "GET" "/api/hello/:name" (req) {
    let name = req.params.name
    return { "message": f"Hello, {name}!" }
}

// Query parameter: curl http://localhost:8080/api/greet?name=Bob
route "GET" "/api/greet" (req) {
    let name = req.params.name
    if name == nil {
        return { "message": "Hello, world!" }
    }
    return { "message": f"Hello, {name}!" }
}
`
	if err := os.WriteFile(filepath.Join(name, "main.srv"), []byte(mainSrv), 0644); err != nil {
		fmt.Printf("Failed to write main.srv: %v\n", err)
		os.Exit(1)
	}

	// config.yml
	configYml := `server:
  port: "8080"

log:
  level: "info"
  format: "text"
`
	if err := os.WriteFile(filepath.Join(name, "config.yml"), []byte(configYml), 0644); err != nil {
		fmt.Printf("Failed to write config.yml: %v\n", err)
		os.Exit(1)
	}

	// test file
	testSrv := `test "health check returns ok" {
    // TODO: add your tests here
    assert true
}
`
	if err := os.WriteFile(filepath.Join(name, "main_test.srv"), []byte(testSrv), 0644); err != nil {
		fmt.Printf("Failed to write main_test.srv: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Created project: %s/\n", name)
	fmt.Println("")
	fmt.Println("  Files:")
	fmt.Println("    main.srv       — Your service (routes, logic)")
	fmt.Println("    main_test.srv  — Tests")
	fmt.Println("    config.yml     — Runtime configuration")
	fmt.Println("")
	fmt.Println("  Get started:")
	fmt.Printf("    cd %s\n", name)
	fmt.Println("    serv run main.srv --watch")
	fmt.Println("")
	fmt.Println("  Then visit: http://localhost:8080/health")
}

// runInitFullStack generates a docker-compose.yml wiring up all Servverse services.
// Usage: serv init --full-stack
func runInitFullStack() {
	const compose = `version: "3.9"

services:
  servgate:
    image: ghcr.io/vyuvaraj/servgate:latest
    ports: ["8080:8080"]
    environment:
      - SERV_MESH_URL=http://servmesh:8083
      - SERV_QUEUE_URL=http://servqueue:8085
      - SERV_STORE_URL=http://servstore:9000
    depends_on: [servmesh, servqueue, servstore]

  servmesh:
    image: ghcr.io/vyuvaraj/servmesh:latest
    ports: ["8083:8083"]

  servqueue:
    image: ghcr.io/vyuvaraj/servqueue:latest
    ports: ["8085:8085"]
    volumes: ["queue_data:/data"]

  servstore:
    image: ghcr.io/vyuvaraj/servstore:latest
    ports: ["9000:9000"]
    volumes: ["store_data:/data"]

  servdb:
    image: ghcr.io/vyuvaraj/servdb:latest
    ports: ["5432:5432"]
    environment:
      - POSTGRES_PASSWORD=servverse
    volumes: ["db_data:/var/lib/postgresql/data"]

  servcache:
    image: ghcr.io/vyuvaraj/servcache:latest
    ports: ["8086:8086"]

  servcron:
    image: ghcr.io/vyuvaraj/servcron:latest
    ports: ["8087:8087"]
    environment:
      - SERV_QUEUE_URL=http://servqueue:8085

  servtrace:
    image: ghcr.io/vyuvaraj/servtrace:latest
    ports: ["4317:4317", "16686:16686"]

  servconsole:
    image: ghcr.io/vyuvaraj/servconsole:latest
    ports: ["8888:8888"]
    environment:
      - SERV_GATE_URL=http://servgate:8080
      - SERV_MESH_URL=http://servmesh:8083
      - SERV_QUEUE_URL=http://servqueue:8085
      - SERV_STORE_URL=http://servstore:9000
      - SERV_DB_URL=http://servdb:5432
      - SERV_CACHE_URL=http://servcache:8086
      - SERV_CRON_URL=http://servcron:8087
      - SERV_TRACE_URL=http://servtrace:4317
    depends_on: [servgate, servmesh, servqueue, servstore, servdb, servcache, servcron, servtrace]

volumes:
  queue_data:
  store_data:
  db_data:
`
	outPath := "docker-compose.yml"
	if err := os.WriteFile(outPath, []byte(compose), 0644); err != nil {
		fmt.Printf("Failed to write docker-compose.yml: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ Generated docker-compose.yml with all Servverse services.")
	fmt.Println("")
	fmt.Println("  Start the full stack:")
	fmt.Println("    docker compose up -d")
	fmt.Println("")
	fmt.Println("  ServConsole dashboard → http://localhost:8888")
	fmt.Println("  ServGate API gateway  → http://localhost:8080")
	fmt.Println("  ServTrace UI          → http://localhost:16686")
}

