# ServAuth Roadmap

This roadmap outlines the planned development phases for the ServAuth identity provider service.

---

## Phase 1: Core Authentication (In Progress)
- [x] **User registration & login** — Email/password signup and login endpoints. [June 29, 2026]
- [x] **OAuth2/OIDC provider** — Authorization code flow and client credential token issuance. [June 29, 2026]
- [x] **Password reset & account lockout** — Recovery flows and lockout gates. [June 29, 2026]
- [ ] **Serv-lang integration** — `auth.*` builtin helpers.

## Phase 2: Advanced Directory Capabilities
- [ ] **Multi-tenant directories** — Isolated pools.
- [ ] **Social login federation** — Google/GitHub integration.
- [ ] **MFA support** — TOTP / WebAuthn.
- [ ] **Session management** — Active session revocation.
