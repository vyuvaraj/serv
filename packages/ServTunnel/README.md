# ServTunnel

```bash
docker run -p 8092:8092 ghcr.io/vyuvaraj/servtunnel:latest
```

`ServTunnel` is a secure, instant tunneling service for exposing local Serv services to the internet during development. It is part of the **Serv-verse** ecosystem.

One command creates a public URL that forwards requests to your local machine — ideal for webhook testing, OAuth callbacks, and sharing work-in-progress.

---

## Key Features

* **Subdomain-based routing**: Each tunnel gets a unique subdomain (e.g., `myapp.servverse.net`).
* **WebSocket transport**: Firewall-friendly, no special network configuration needed.
* **Request inspection**: Built-in ring buffer captures requests for debugging and replay.
* **OTel propagation**: Forwards `traceparent` headers natively through the tunnel.
* **Colorful terminal output**: Real-time request log with status codes and latency.
* **Health & telemetry**: Standard `/healthz`, `/readyz` endpoints via ServShared.

---

## Project Structure

```
ServTunnel/
├── pkg/
│   ├── server/
│   │   └── server.go      # Relay server: HTTP listener + WebSocket hub
│   ├── client/
│   │   └── client.go      # CLI client: WS connection + local HTTP proxy
│   ├── tunnel/
│   │   └── protocol.go    # Wire protocol types (JSON-framed messages)
│   ├── inspector/
│   │   └── inspector.go   # Request inspection ring buffer + REST API
│   └── otel/
│       └── otel.go        # ServShared OTel delegation
├── main.go                # Entry point with server/client subcommands
├── main_test.go           # Integration tests
├── ROADMAP.md             # Feature planning
└── README.md              # This documentation
```

---

## Quick Start

### 1. Build

```bash
go build -o servtunnel .
```

### 2. Start the Relay Server

```bash
./servtunnel server --port 8443 --domain localhost
```

The relay will listen on `:8443` and route requests based on subdomain in the `Host` header.

### 3. Start a Tunnel Client

In another terminal, with a local service running on port 8080:

```bash
./servtunnel client 8080 --relay ws://localhost:8443/ws/connect --subdomain myapp
```

You'll see:

```
  ╔═══════════════════════════════════════╗
  ║         ServTunnel Client              ║
  ╚═══════════════════════════════════════╝

  Local service:  http://localhost:8080
  Relay server:   ws://localhost:8443/ws/connect

  ✓ Tunnel established!

  Public URL:     http://myapp.localhost:8443
  Subdomain:      myapp

  ─────────────────────────────────────────
  Forwarding requests... (Ctrl+C to stop)
  ─────────────────────────────────────────
```

### 4. Send Requests Through the Tunnel

```bash
curl -H "Host: myapp.localhost" http://localhost:8443/api/hello
```

The request will be forwarded to `http://localhost:8080/api/hello` through the tunnel.

---

## Standalone Tunnel (ngrok / Localtunnel Alternative)

ServTunnel works as a generic, zero-dependency, self-hosted alternative to ngrok, localtunnel, or cloudflared. You can use it to expose **any local HTTP server** (Python Flask, Node Express, React Dev Server, Go API, PHP, etc.) to the local network or public internet.

### Expose Any Local App/Service

Simply compile or download `servtunnel`, launch your local application, and run the tunnel client:

1. **Start your local web app** (e.g. Node Express on port `3000`):
   ```bash
   npm run dev # listening on http://localhost:3000
   ```

2. **Expose port 3000** using a public or shared ServTunnel relay server:
   ```bash
   ./servtunnel client 3000 --relay ws://relay.yourdomain.com:8443/ws/connect --subdomain dev-app
   ```

3. **Access publicly**:
   Your app is now securely exposed at:
   `http://dev-app.relay.yourdomain.com:8443`


## Management APIs

### List Active Tunnels

```bash
curl http://localhost:8443/api/tunnels
```

### Inspect Captured Requests

```bash
curl http://localhost:8443/api/inspect
```

### Get a Single Captured Request

```bash
curl http://localhost:8443/api/inspect/req-1
```

### Health Check

```bash
curl http://localhost:8443/healthz
curl http://localhost:8443/readyz
```

---

## Architecture

```
External Service (Stripe, etc.)
        │ HTTPS
        ▼
┌─────────────────────────────┐
│   ServTunnel Relay Server   │
│  subdomain.servverse.net    │
├─────────────────────────────┤
│ Host header → subdomain     │
│ → lookup WebSocket conn     │
│ → forward as JSON frame     │
└──────────────┬──────────────┘
               │ WebSocket
               ▼
┌─────────────────────────────┐
│   ServTunnel Client (CLI)   │
│   servtunnel client 8080    │
├─────────────────────────────┤
│ Receives JSON frame         │
│ → HTTP request to localhost │
│ → sends response back       │
└─────────────────────────────┘
```

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVTUNNEL_ADDR` | `:8443` | Server listen address |
| `SERVTUNNEL_DOMAIN` | `localhost` | Base domain for subdomains |
| `SERVTUNNEL_RELAY` | `ws://localhost:8443/ws/connect` | Client relay URL |
| `OTEL_ENDPOINT` | (none) | OpenTelemetry collector endpoint |

---

## Verification

Run the test suite:

```bash
go test ./... -v
```
