# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.1.0] - 2026-07-17

### Added
- Implemented `/api/message/flow` endpoint for tracking visual message timelines.
- Implemented `/api/incidents/postmortem` endpoint for automated incident postmortem generation.

## [1.0.0] - 2026-07-15

### Added
- Standardized error format returning JSON structure (error, code, and 	race_id).
- Implemented /api/v1/ endpoint prefix support.
- Configured global protection middlewares: TraceMiddleware, RateLimitMiddleware, CORSMiddleware, MaxBytesMiddleware, AuthMiddleware, and TenantMiddleware.
- Upgraded and pinned all internal ecosystem dependency versions to target v1.0.0.
