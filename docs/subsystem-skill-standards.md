# Subsystem Skill Standards

- Generated on: 2026-04-11
- Project root: `E:/123pan/Downloads/codex2api`
- Fixed priority order:
  1. Current user hard constraints
  2. Real entrypoints, real behavior, real business results, existing tests
  3. Existing repo docs, README, deploy config
  4. This file
  5. Bound stack/domain skills
  6. General standard skills
  7. Historical notes

## Subsystems

### 1. Bootstrap and deployment
- Scope: `main.go`, `config/`, `Dockerfile`, `docker-compose*.yml`, `.env*.example`, `docs/DEPLOYMENT.md`
- Skills: `codebase-onboarding`, `golang-patterns`, `deployment-patterns`, `docker-patterns`, `safety-guard`, `verification-loop`
- Why: startup chain, config source, Docker network, update flow, health checks
- Forbidden:
  - do not silently replace PostgreSQL or Redis
  - do not confuse source rebuild with image pull
  - do not keep bad legacy deploy glue if it hurts the real path
- Acceptance: health check works, config source is clear, update path is reproducible

### 2. Admin API
- Scope: `admin/`, `api/`, `security/`
- Skills: `golang-patterns`, `api-design`, `security-review`, `coding-standards`, `verification-loop`
- Why: request validation, auth, status codes, runtime settings save path
- Acceptance: `/api/admin/*` behavior stays consistent and tested

### 3. Runtime settings and scheduling
- Scope: `auth/`, `proxy/`, `cache/`, runtime settings logic
- Skills: `golang-patterns`, `coding-standards`, `benchmark`, `security-review`, `verification-loop`
- Why: concurrency, retry, refresh, cleanup, proxy routing, env override behavior
- Forbidden:
  - no new compatibility shells
  - env override must not pollute database fallback values
- Acceptance: no panic, no behavior drift, no performance regression on key paths

### 4. Persistence and database
- Scope: `database/`, parts of `config/`, pool settings
- Skills: `golang-patterns`, `database-migrations`, `postgres-patterns`, `verification-loop`
- Why: schema, upsert behavior, pool settings, fallback storage semantics
- Acceptance: existing schema preserved, persistence remains predictable

### 5. Frontend admin UI
- Scope: `frontend/src/`
- Skills: `frontend-patterns`, `coding-standards`, `verification-loop`, `browser-qa`
- Why: login gate, settings page, tables, state flow, user-visible regressions
- Acceptance: `/admin/` stays stable, build passes, critical UI flows remain intact

### 6. Docs and review records
- Scope: `README.md`, `docs/*.md`
- Skills: `architecture-decision-records`, `workspace-surface-audit`, `verification-loop`
- Why: docs must match current code and deployment shape
- Acceptance: docs are readable, current, and free of broken placeholder text
