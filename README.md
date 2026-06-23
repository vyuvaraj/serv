# ServRegistry

ServRegistry is the lightweight, S3-backed community package hub and registry server for the Servverse ecosystem. It allows sharing, versioning, and resolving packages written for `serv-lang` microservices.

## Features

- **S3 / ServStore Backend**: Packages are stored as tarballs in a dedicated S3 bucket (or `ServStore`).
- **Dependency Resolution**: Exposes APIs to resolve package dependency trees dynamically.
- **Token Authorization**: Supports JWT signature verification to protect package publication.
- **Ecosystem Landing Dashboard**: Built-in web dashboard displaying active packages, sizes, and versions.

## API Endpoints

### 1. Health Checks
- `GET /healthz` - Health probe.
- `GET /readyz` - Readiness probe.

### 2. Publish Package
- `POST /publish` or `POST /api/v1/publish`
  - Uploads a package tarball (`.tar.gz`).
  - Expects a `serv.toml` manifest file in the root of the archive to parse the package name, version, and dependencies.
  - If `SERV_JWT_SECRET` is enabled, requires a valid token via the `Authorization: Bearer <token>` header.

### 3. Fetch Package Tarball
- `GET /packages/{name}.tar.gz` or `GET /api/v1/packages/{name}.tar.gz`
  - Fetches the latest published version of the package.
- `GET /packages/{name}/{version}/{name}-{version}.tar.gz` or `GET /api/v1/packages/{name}/{version}/{name}-{version}.tar.gz`
  - Fetches a specific version of the package.

### 4. Search Packages
- `GET /api/packages/search?q={query}` or `GET /api/v1/packages/search?q={query}`
  - Returns a list of packages matching the query string.

### 5. Listing and Dependencies
- `GET /api/packages/` or `GET /api/v1/packages/`
  - Lists all packages in the registry.
- `GET /api/packages/{name}/versions` or `GET /api/v1/packages/{name}/versions`
  - Retrieves all published versions of a package.
- `GET /api/packages/{name}/deps` or `GET /api/packages/{name}/deps`
  - Returns the resolved dependency tree for the latest package version.
- `GET /api/packages/{name}/{version}/deps` or `GET /api/packages/{name}/{version}/deps`
  - Returns the resolved dependency tree for a specific version.

## Configuration (Environment Variables)

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Local server port | `8088` |
| `SERV_STORE_ENDPOINT` | ServStore or external S3 URL | `http://localhost:9000` |
| `SERV_STORE_ACCESS_KEY` | Access key for S3 bucket | `admin` |
| `SERV_STORE_SECRET_KEY` | Secret key for S3 bucket | `admin123` |
| `SERV_JWT_SECRET` | Secret key to validate signature for publishing | *(Disabled)* |

## Running Locally

```bash
go run main.go --addr :8088 --s3-endpoint http://localhost:9000
```
