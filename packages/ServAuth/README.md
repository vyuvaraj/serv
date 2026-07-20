# ServAuth

```bash
docker run -p 8086:8086 ghcr.io/vyuvaraj/servauth:latest
```

ServAuth is the identity provider and access token validation service of the Servverse ecosystem.

## Features
- **User Registration & Login**: User registration and login validation.
- **OIDC/OAuth2 Provider**: OIDC token generation endpoint (`POST /oauth/token`) supporting the `client_credentials` flow.
- **Password Reset & Account Lockout**: Safe recovery token resets and automated account locking for 5 minutes after 3 consecutive failed login attempts.

## API Endpoints
- `POST /api/auth/register` - Create a new user profile
- `POST /api/auth/login` - Validate credentials and return session token
- `POST /oauth/token` - OAuth2 OIDC client credentials token grant
- `POST /api/auth/reset-password/request` - Generate recovery token
- `POST /api/auth/reset-password/confirm` - Reset password using confirmation token

## Getting Started
To run the integration tests locally:
```bash
go test -v ./...
```
