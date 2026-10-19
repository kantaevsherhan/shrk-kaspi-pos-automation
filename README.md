# Kaspi POS Console

Отдельный сервис для счетов, QR-оплат, истории операций и возвратов Kaspi Pay. Сервер написан на Go, панель — на Vue 3 и Element Plus. Другие ваши сервисы обращаются к API с `Authorization: Bearer <ключ>`.

> Интеграция использует внутренние интерфейсы Kaspi Pay. Они могут измениться. Проверка с реальным аккаунтом Kaspi для этого переписывания не проводилась.

## Запуск

Требуются Go 1.27+ и Node.js 22.18+ для сборки панели. Для запуска собранного сервиса Node.js не нужен.

1. Скопируйте `.env.example` в `.env`.
2. Задайте `TOKEN_SECRET_KEY` (случайные 32 байта в hex, 64 символа) и `SERVICE_API_KEYS` (JSON с отдельным случайным ключом от 32 символов для каждого вашего сервиса).
3. Соберите панель: `cd web && corepack pnpm install --frozen-lockfile && corepack pnpm build`.
4. Запустите сервер из корня проекта: `go run ./cmd/kaspi-pos`.
5. Откройте `http://127.0.0.1:3000`, введите один из API-ключей и подключите Kaspi Pay через SMS.

В PowerShell случайный ключ можно создать так:

```powershell
[Convert]::ToHexString((1..32 | ForEach-Object { Get-Random -Maximum 256 }))
```

Для Docker достаточно заполнить `.env` и выполнить `docker compose up -d --build`. Compose публикует порт только локально; для доступа извне настройте HTTPS-прокси.

## API

Все маршруты `/api/*` требуют Bearer-ключ. `GET /health` доступен без ключа.

```bash
curl -X POST http://127.0.0.1:3000/api/invoice/create \
  -H 'Authorization: Bearer YOUR_SERVICE_KEY' \
  -H 'Content-Type: application/json' \
  -d '{"phoneNumber":"77001234567","amount":1000,"comment":"Заказ 42"}'
```

Маршруты и примеры: [документация API](docs/API.md).

## Данные и безопасность

В `STATE_DIR` (по умолчанию `data/`) хранятся идентификатор устройства, ключ ECDSA, зашифрованная сессия Kaspi, **только незавершённые** платежи и очередь повторов вебхуков. Завершённую историю панель получает из Kaspi. Журнал запросов по сервисам и SQLite удалены. Сохраняйте `STATE_DIR` и `TOKEN_SECRET_KEY` вместе при резервном копировании.

После перехода со старой Node-версии войдите в Kaspi повторно по SMS: прежняя сессия в SQLite больше не используется. Незавершённые платежи и очередь повторов из JSON-файлов переносятся автоматически.

Файл `STATE_DIR/webhooks.json` настраивается по [примеру](webhooks.example.json). События `payment.success`, `payment.failed`, `payment.expired`, `payment.lost` отправляются POST-запросом с `X-Webhook-Signature: sha256=<HMAC-SHA256>`. Неуспешная доставка повторяется до трёх попыток. Платежи проверяются каждые 3 секунды.

Все именованные API-ключи имеют полный доступ к одному подключённому аккаунту Kaspi Pay, включая возвраты. Не передавайте ключи в публичные клиенты и открывайте сервис наружу только через HTTPS. Панель хранит введённый ключ в `sessionStorage` вкладки.

## Разработка

```bash
go test ./cmd/... ./internal/...
cd web
corepack pnpm dev
```

Vite перенаправляет `/api` и `/health` на Go-сервер `127.0.0.1:3000`. Сборка выполняется командой `corepack pnpm build`.

Если Kaspi отклонит вход с кодом `OldVersionToUpdate`, обновите параметры `APP_VERSION`, `APP_BUILD` и устройства в `.env` согласно используемой версии Kaspi Pay.
