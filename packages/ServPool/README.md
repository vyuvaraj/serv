# ServPool

```bash
docker run -p 8087:8087 ghcr.io/vyuvaraj/servpool:latest
```

ServPool is a database connection pooler and query routing proxy service of the Servverse ecosystem.

## Features
- **Connection Pooling**: Multiplexes and manages active pooled database connections to reduce startup overhead.
- **Read/Write Query Routing**: Automatically splits incoming operations (routing mutations to the Primary pool and read queries starting with `SELECT` to the Replica pool).
- **Multi-Database Dialects**: Dialect safety checks supporting PostgreSQL and MySQL parameter format checks.

## API Endpoints
- `POST /api/db/query` - Route and execute an SQL statement
- `GET /api/db/stats` - Fetch connection pooling and query statistics per pool

## Getting Started
To run the integration tests locally:
```bash
go test -v ./...
```

---

## Use Without Servverse (Standalone Quickstart)

`ServPool` can be used as a standalone database connection pooler and query router proxy (similar to PgBouncer, but with a REST interface):

1. **Configure your Database backend** connection details via environment variables:
   ```bash
   export DB_PRIMARY_URL="postgres://user:pass@localhost:5432/primary?sslmode=disable"
   export DB_REPLICA_URL="postgres://user:pass@localhost:5432/replica?sslmode=disable"
   ```

2. **Start ServPool**:
   ```bash
   go run main.go --port 8087 --dialect postgres
   ```

3. **Execute SQL Statements** via the REST endpoint:
   ```bash
   curl -X POST http://localhost:8087/api/db/query \
     -H "Content-Type: application/json" \
     -d '{"query": "SELECT * FROM users;"}'
   ```

4. **Monitor Pool Statistics** (active connections, execution counts, replica splits):
   ```bash
   curl http://localhost:8087/api/db/stats
   ```


