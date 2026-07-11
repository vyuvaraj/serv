# ServQueue

```bash
docker run -p 8082:8082 -p 61613:61613 ghcr.io/vyuvaraj/servqueue:latest
```

`ServQueue` is a lightweight, distributed-ready message broker tailored for the **Serv** ecosystem. Its primary differentiating feature is **Compute-in-Queue** (Native WASM Stream Processing): the ability to run lightweight, compiled WebAssembly (WASI) modules inline inside the messaging pipeline to filter, enrich, or transform payloads dynamically before they reach subscribers.

---

## Key Features

* **WASM Transform Engine**: Leverage a sandboxed, pure-Go WASM runtime (`wazero`) to execute inline stream processing filters on topics.
* **STOMP Protocol Server**: Built-in TCP endpoint (`tcp://localhost:61613`) supporting standard STOMP subscription frames (`CONNECT`, `SUBSCRIBE`, `SEND`, `DISCONNECT`).
* **HTTP REST API**: Publish messages, subscribe, clear configurations, and query stats over HTTP (`http://localhost:8082`).
* **Telemetry & Context**: Out-of-the-box support for distributed trace propagation and execution logging.

---

## Project Structure

```
ServQueue/
├── pkg/
│   ├── broker/
│   │   ├── engine.go     # Message dispatch, subscriber routing, & transform hooks
│   │   └── wasm.go       # Wazero integration for WASI execution sandboxing
│   ├── stomp/
│   │   └── server.go     # STOMP protocol frame decoder/encoder & TCP server
│   └── web/
│       └── server.go     # HTTP JSON administration & publish endpoints
├── main.go               # Entrypoint & bootstrap configuration
├── ROADMAP.md            # Feature planning and progression tracker
└── README.md             # This documentation
```

---

## Quick Start

### 1. Build and Run
Ensure you have Go installed, then compile and run:
```bash
go build -o servqueue.exe main.go
./servqueue.exe
```
* The **STOMP TCP Server** listens on `:61613`
* The **HTTP Management API** listens on `:8082`

> [!IMPORTANT]
> **Default Authentication Credentials:**
> - **Username**: `admin`
> - **Password**: `secret`
> These credentials must be passed in the headers of your STOMP frames (e.g. `login: admin`, `passcode: secret`) or REST operations.


### 2. HTTP Admin API Usage

#### Publish a Message
```bash
curl -X POST http://localhost:8082/api/publish \
  -H "Content-Type: application/json" \
  -d '{"topic": "orders", "payload": "hello world"}'
```

#### Register a WASM Transformation Module
Register a compiled `.wasm` file to automatically process all messages sent to a specific topic before delivery:
```bash
curl -X POST http://localhost:8082/api/topics/orders/transform \
  --data-binary @my_transform.wasm
```

#### Get Broker Stats
```bash
curl http://localhost:8082/api/stats
```

---

## Verification

Run the integration test suite:
```bash
go test ./... -v
```

---

## Use Without Servverse (Standalone Quickstart)

`ServQueue` can function as a standalone, independent STOMP message broker:
1. Run the broker:
   ```bash
   ./servqueue --port 8082 --stomp-port 61613
   ```
2. Connect using any standard STOMP client library (Python `stomp.py`, Go `stomp`, Node `stompjs`) to port `61613` using:
   - Username: `admin`
   - Password: `secret`

---

## STOMP Client Compatibility Guide (SA.18)

`ServQueue` implements a compliant STOMP v1.1/v1.2 protocol server. You can subscribe and publish using any generic library.

### 1. Python (`stomp.py`)
```python
import stomp
import time

class MyListener(stomp.ConnectionListener):
    def on_message(self, frame):
        print(f"Received message: {frame.body}")

conn = stomp.Connection([('127.0.0.1', 61613)])
conn.set_listener('', MyListener())
conn.connect('admin', 'secret', wait=True)

# Subscribe to a topic
conn.subscribe(destination='orders', id='sub-1', ack='auto')

# Publish a message
conn.send(body='{"order_id": 42}', destination='orders')

time.sleep(2)
conn.disconnect()
```

### 2. Spring / Java
Configure your STOMP client settings:
```java
WebSocketClient client = new StandardWebSocketClient();
WebSocketStompClient stompClient = new WebSocketStompClient(client);
stompClient.setMessageConverter(new StringMessageConverter());

StompHeaders headers = new StompHeaders();
headers.add("login", "admin");
headers.add("passcode", "secret");

stompClient.connect("ws://localhost:8082/stomp", new StompSessionHandlerAdapter() {
    @Override
    public void afterConnected(StompSession session, StompHeaders connectedHeaders) {
        session.subscribe("orders", new StompFrameHandler() {
            @Override
            public Type getPayloadType(StompHeaders headers) {
                return String.class;
            }
            @Override
            public void handleFrame(StompHeaders headers, Object payload) {
                System.out.println("Received: " + payload);
            }
        });
        session.send("orders", "hello from spring");
    }
}, headers);
```

### 3. Go (`go-stomp`)
```go
package main

import (
	"log"
	"github.com/go-stomp/stomp/v3"
)

func main() {
	options := []func(*stomp.Conn) error{
		stomp.Conn.Login("admin", "secret"),
	}

	conn, err := stomp.Dial("tcp", "127.0.0.1:61613", options...)
	if err != nil {
		log.Fatalf("cannot connect to STOMP: %v", err)
	}
	defer conn.Disconnect()

	// Subscribe
	sub, err := conn.Subscribe("orders", stomp.AckAuto)
	if err != nil {
		log.Fatalf("cannot subscribe: %v", err)
	}

	// Publish
	err = conn.Send("orders", "text/plain", []byte("hello from go"), nil)
	if err != nil {
		log.Fatalf("cannot send message: %v", err)
	}

	// Read message
	msg := <-sub.C
	log.Printf("Received message: %s", string(msg.Body))
}
```

### 4. JavaScript / Browser (`stompjs`)
```javascript
import { Client } from '@stomp/stompjs';

const client = new Client({
    brokerURL: 'ws://localhost:8082/stomp', // or tcp endpoint via proxy
    connectHeaders: {
        login: 'admin',
        passcode: 'secret',
    },
    debug: function (str) {
        console.log(str);
    },
    onConnect: () => {
        client.subscribe('orders', message => {
            console.log(`Received payload: ${message.body}`);
        });

        client.publish({ destination: 'orders', body: 'hello browser' });
    },
});

client.activate();
```
