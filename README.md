# servlockctl

`servlockctl` is the standalone, open-source command-line interface (CLI) administration client for **ServLock** (Distributed Lock Manager).

It allows you to acquire, release, renew, and query lock leases directly from your terminal.

## Installation

```bash
go build -o servlockctl.exe .
```

## Configuration

You can configure the client using flags or environment variables:

| Flag | Env Variable | Description | Default |
|---|---|---|---|
| `--url` | `SERVLOCK_URL` | ServLock server URL | `http://localhost:8089` |
| `--api-key` | `SERVLOCK_API_KEY` | API Key for authorization | (None) |
| `--tenant` | - | Tenant ID for request isolation | `default` |

## Usage Commands

### 1. Acquire a lock
```bash
servlockctl acquire --key my-resource --ttl 10000 --owner cli-user
```
Returns lock status and lease parameters (fencing token, owner).

### 2. Release a lock
```bash
servlockctl release --key my-resource --owner cli-user --fencing-token 12345
```

### 3. Renew a lock lease
```bash
servlockctl renew --key my-resource --owner cli-user --fencing-token 12345 --ttl 10000
```

### 4. Query active locks status
```bash
servlockctl status
```
Returns a JSON array of all active locks, waiters, and lease diagnostics.

### 5. Check Health
```bash
servlockctl health
```

## License
Licensed under the GNU Affero General Public License v3.0 (AGPL-3.0). See [LICENSE](./LICENSE) for details.
