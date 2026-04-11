# Review Loop Log

- Project: `codex2api`
- Root: `E:/123pan/Downloads/codex2api`
- Note: historical mojibake content was removed; only current useful conclusions remain.

## Round 1 - Baseline lock
- Locked the target repo and identified the stack and real entrypoints.
- Confirmed key paths: `/admin/`, `/health`, `/api/admin/*`.

## Round 2 - Admin auth stability
- Fixed admin auth limiter cleanup panic risk.
- Verified backend and frontend baselines.

## Round 3 - Auth gate correctness
- Fixed `AuthGate` so non-success health responses are no longer treated as authenticated.
- Added targeted frontend regression coverage.

## Round 4 - CORS tightening
- Tightened same-origin behavior and aligned docs.
- Kept real entrypoints unchanged.

## Round 5 - Single-user deployment review
- Re-reviewed under the accepted single-user/self-hosted exposure boundary.
- Fixed `Store.Stop()` idempotency to avoid double-close panic on shutdown.

## Round 6 - Env-first runtime settings
- Implemented option 2: env and `.env` win at runtime, database settings remain fallback.
- Added `config.ApplySystemSettingsEnvOverrides()`.
- Reapplied env overrides on startup and after settings saves.
- Fixed fallback pollution when saving unrelated fields.
- Verified with `go test ./config ./admin ./security -count=1` and `go test ./... -count=1`.

## Round 7 - Text cleanup
- Removed broken placeholder question-mark strings from logs, README, and deploy/config docs.
- Rewrote internal docs in clean UTF-8/ASCII-safe content.
- Goal: remove visible mojibake and restore maintainable project text.
