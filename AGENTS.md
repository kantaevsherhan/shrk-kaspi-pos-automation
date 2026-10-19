# Project Guidelines

## Build & Configuration

- **Node.js ≥ 22.13** required (ES modules and built-in SQLite).
- Create `.env` with `TOKEN_SECRET_KEY` (64-char hex, 32 bytes) and `SERVICE_API_KEYS` before first run — see `.env.example`.
- On first `npm start`, `keypair.json` and `device.json` are auto-generated (gitignored).
- Regenerate via `npm run regen:keypair` / `npm run regen:device` (invalidates sessions).

## Architecture

- **Entry point**: `server.js` — Express, routes under `/api/*`, static `public/`.
- **Source**: `src/config.js`, `src/crypto.js`, `src/helpers.js`, `src/session.js`.
- **Routes**: `src/routes/{auth,invoice,qr,history,refund}.js`.
- **Server-side session after auth** — temp in-memory `Map` only during 3-step SMS flow; after that, encrypted Kaspi session and request audit live in SQLite. Service callers use Bearer keys.

## Code Style

- **ES Modules** (`import`/`export`), semicolons, `const`, arrow functions.
- `async/await` with try/catch, JSON `{ error }` on failure.
- ESLint configured (`eslint.config.js`).
