# ServQueue Roadmap

This document outlines the planned evolutionary stages of **ServQueue** to evolve from a lightweight local broker into a distributed, high-performance messaging platform with inline compute capabilities.

---

## Phase 1: Core Foundation & WASM Integration (Completed)
- [x] **Thread-safe Pub/Sub engine**: Core routing structure mapping topics to active subscription channels.
- [x] **STOMP TCP Server**: Wire protocol parsing (`CONNECT`, `SUBSCRIBE`, `SEND`) for direct compatibility with Serv-lang.
- [x] **HTTP Management API**: Control endpoints for manual publishing and WASM filter attachment.
- [x] **WASM Sandbox Integration**: Wazero-based runner that passes messages through compiled `.wasm` modules.

---

## Phase 2: WASM Performance & Shared State
- [ ] **Module Caching**: Maintain compiled modules in-memory to eliminate instantiation latency on subsequent messages.
- [ ] **Stateful Transforms**: Allow WASM functions to access or persist key-value pairs (using a host-function map) to support stateful aggregations (e.g., rate calculation, windowing).
- [ ] **Direct memory passing**: Transition stdin/stdout pipes to guest-allocated shared buffers to reduce copying overhead.

---

## Phase 3: Cluster Consensus & Distributed Replication
- [ ] **Raft-backed Clustering**: Implement Hashicorp Raft to replicate topic definitions and registered transforms across a 3-node cluster.
- [ ] **Partitioned Queues**: Support message partitioning based on routing keys.
- [ ] **High Availability**: Dynamic subscriber re-routing when a broker node drops.

---

## Phase 4: ServStore Tiered Storage (Infinite Backlog Retention)
- [ ] **Write-Ahead Log (WAL)**: Record hot incoming messages to a local disk WAL.
- [ ] **Cold Data Offloading**: Automatically roll WAL segments into structured segment files and upload them to `ServStore` / S3.
- [ ] **Log Replay**: Enable client replay requests (e.g., `replay?since=timestamp`), pulling cold segments back from S3.

---

## Phase 5: Ecosystem & Dashboard Integration
- [ ] **ServConsole Integration**: Feed broker throughput, active subscriptions, and WASM performance stats directly to the central Serv dashboard.
- [ ] **Distributed Trace Mapping**: Automatically attach OpenTelemetry tracing headers to messages to visualize message spans.
