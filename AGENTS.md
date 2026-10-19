# Project guidelines

- Backend: Go 1.27+, standard library, entry point `cmd/kaspi-pos/main.go`, code in `internal/app/`.
- Frontend: Vue 3, Element Plus, Vite, source in `web/src/`; build to `web/dist/`.
- Configure `.env` with `TOKEN_SECRET_KEY` (64 hex characters) and `SERVICE_API_KEYS` (JSON object with named random keys, each at least 32 characters).
- Run `go test ./cmd/... ./internal/...` and `cd web && pnpm build` after changes.
- `STATE_DIR` stores the device identity, encrypted Kaspi session, pending payments and webhook retries. Do not add a SQLite audit journal or persist completed payment history; read history from Kaspi.
- Never log tokens, OTPs, signed headers, API keys or full Kaspi response bodies.
