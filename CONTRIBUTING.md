# Разработка

Требуются Go 1.27+ и Node.js 22.18+. Запуск сервера: `go run ./cmd/kaspi-pos`. Панель: `cd web && corepack pnpm install --frozen-lockfile && corepack pnpm dev`. Перед коммитом запускайте `go test ./cmd/... ./internal/...` и `corepack pnpm build` в `web/`.

Не добавляйте в Git `.env`, `data/`, ключи и реальные ответы Kaspi. Проверяйте сетевое поведение через локальные mock-серверы; реальные платежи и SMS требуют отдельного ручного теста с аккаунтом владельца.
