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
