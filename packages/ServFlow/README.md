# ServFlow

```bash
docker run -p 8089:8089 ghcr.io/vyuvaraj/servflow:latest
```

ServFlow is a stateful DAG-based workflow orchestrator and Saga compensation engine of the Servverse ecosystem.

## Features
- **DAG execution**: Runs multi-step execution graphs sorted topologically by dependency constraints.
- **Durable execution**: Checkpoints workflow state to `.state` files on disk so executions survive engine restarts.
- **Saga rollback compensation**: Triggers rollback tasks (`CompensateAction`) in reverse order of completed steps on failure.

## API Endpoints
- `POST /api/workflows/define` - Define a new DAG workflow structure
- `POST /api/workflows/execute` - Execute a workflow instance
- `POST /api/workflows/resume` - Resume execution of a failed/stopped workflow from a checkpoint file
- `GET /api/workflows/instances/{id}` - Fetch logs and step statuses of an execution instance

## Getting Started
To run the integration tests locally:
```bash
go test -v ./...
```

---

## Use Without Servverse (Standalone Quickstart)

`ServFlow` can run as an independent DAG workflow coordinator with local file checkpoints:
1. Start `ServFlow` in standalone mode:
   ```bash
   ./servflow --port 8089 --standalone --checkpoint-dir ./state
   ```
2. Register a simple workflow:
   ```bash
   curl -X POST http://localhost:8089/api/workflows/define \
     -H "Content-Type: application/json" \
     -d '{
       "id": "demo-flow",
       "tasks": [
         {"name": "TaskA", "action": "success"},
         {"name": "TaskB", "depends_on": ["TaskA"], "action": "success"}
       ]
     }'
   ```
3. Execute the workflow:
   ```bash
   curl -X POST http://localhost:8089/api/workflows/execute \
     -H "Content-Type: application/json" \
     -d '{"workflow_id": "demo-flow"}'
   ```

