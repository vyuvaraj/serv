# ServAuth Roadmap

This roadmap outlines the planned development phases for the ServAuth identity provider service.

---

## Phase 1: Core Authentication (In Progress)
- [x] **User registration & login** â€” Email/password signup and login endpoints. [June 29, 2026]
- [x] **OAuth2/OIDC provider** â€” Authorization code flow and client credential token issuance. [June 29, 2026]
- [x] **Password reset & account lockout** â€” Recovery flows and lockout gates. [June 29, 2026]
- [x] **Serv-lang integration** â€” `auth.*` builtin helpers. [June 29, 2026]

## Phase 2: Advanced Directory Capabilities
- [x] **Multi-tenant directories** â€” Isolated pools. [June 29, 2026]
- [x] **Social login federation** â€” Google/GitHub integration. [June 29, 2026]
- [x] **MFA support** â€” TOTP / WebAuthn. [June 29, 2026]
- [x] **Session management** â€” Active session revocation. [June 29, 2026]
- [x] **API key issuance** â€” Scoped service accounts API keys. [June 29, 2026]
- [x] **User management API** â€” Admin user CRUD and session lists. [June 29, 2026]
- [x] **Secrets Envelope encryption** â€” KMS encryption simulator. [June 29, 2026]

## Phase 3: Production Hardening & Resilience (Completed)
- [x] **State-Isolated Unit & Validation Tests** â€” Implement table-driven tests for verification of key validation and lockouts. [June 30, 2026]
- [x] **Interface Abstractions & Decoupled Storage** â€” Extract storage access behind `UserStore` interface for mockability. [June 30, 2026]
- [x] **Structured Logging & OTel Tracing** â€” Add TraceMiddleware for tracing context propagation and JSON log format. [June 30, 2026]
- [x] **SIGTERM Graceful Shutdown** â€” Register listener to shut down HTTP listener cleanly with a 5-second timeout. [June 30, 2026]

## Phase 4: Architectural Depth (Pending)
- [x] **Secrets Envelope Key Rotation** â€” Secret KMS rotation schedule & API key hashing (SEC.8)

## Phase 5: Production Security & Contract Hardening (Pending)
- [ ] **JWT Key Rotation via JWKS** — Replace single shared SERV_JWT_SECRET with a JWKS endpoint; all services verify tokens by fetching the public key, enabling rotation without restarts (SEC.9)
- [ ] **Secret Redaction in Logs** — ServShared.SanitizeLog() strips tokens/keys/passwords before emission (SEC.10)
- [ ] **Secret Versioning** — KMS stores key versions; encryption always uses latest; decryption accepts any active version (SEC.11)
- [x] **Tenant JWT Claim Enforcement** — Middleware verifies X-Tenant-ID header matches JWT tenant_id claim before any handler runs (SEC.12) [June 30, 2026]
- [ ] **Audit Event Coverage** — Every privileged action (login, key issuance, MFA change) calls EmitAuditEvent; enforced by CI linter (TEST.7)
