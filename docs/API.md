# API Kaspi POS Console

Базовый адрес: `http://127.0.0.1:3000`. Сервисы обращаются к платёжным маршрутам с `Authorization: Bearer <SERVICE_API_KEY>`. Панель использует cookie администратора. Тело POST — JSON. Ответы Kaspi возвращаются в их исходной структуре (`StatusCode`, `Data`, `Message`); при ошибке сервиса возвращается `{ "error": "..." }`. Недоступный Kaspi или HTTP-ошибка upstream дают HTTP 502; некорректный ввод — HTTP 400.

| Метод | Путь | Назначение |
| --- | --- | --- |
| GET | `/health` | Проверка доступности без авторизации |
| GET | `/api/auth/session` | Состояние локальной сессии Kaspi |
| POST | `/api/auth/session` | Совместимый с Node-версией вариант запроса состояния |
| POST | `/api/auth/init` | Начать SMS-вход; вернуть `processId` |
| POST | `/api/auth/send-phone` | `{ "processId":"...", "phoneNumber":"77001234567" }` |
| POST | `/api/auth/verify-otp` | `{ "processId":"...", "otp":"1234" }` |
| POST | `/api/auth/logout` | Удалить сессию Kaspi |
| GET | `/api/session/check` | Проверить сессию запросом к Kaspi |
| GET | `/api/payments/active` | Только незавершённые платежи, отслеживаемые для вебхуков |
| GET | `/api/invoice/client-info?phoneNumber=...` | Сведения о клиенте |
| POST | `/api/invoice/create` | `{ "phoneNumber":"77001234567", "amount":1000, "comment":"Заказ" }` |
| GET | `/api/invoice/details?operationId=...` | Текущий статус счёта |
| POST | `/api/invoice/cancel` | `{ "operationId":"123" }` |
| POST | `/api/invoice/history` | История счетов Kaspi |
| POST | `/api/qr/create` | `{ "amount":1000 }`; необязательно `latitude`, `longitude` |
| GET | `/api/qr/status?qrOperationId=...` | Текущий статус QR |
| POST | `/api/history/operations` | `{ "endDate":"2026-10-19", "lastTransactionDate":"", "statementPeriodCode":0 }` |
| POST | `/api/history/details` | `{ "id":"123", "operationMethod":0 }` |
| POST | `/api/refund/create` | `{ "qrOperationId":"123", "returnAmount":1000 }` |

`/api/qr/create` возвращает `Data.QrToken` — ссылку Kaspi Pay, и `Data.QrOriginalToken` — исходную ссылку для Единого QR. Панель рисует QR локально из `QrOriginalToken`.

ID операций в POST принимаются числом или строкой из десятичных цифр. Суммы принимаются JSON-числом или строкой с числом; допустимы положительные значения до 1 000 000 000. Подключённая сессия Kaspi хранится на сервере, API-ключ не заменяет SMS-подключение.

## Вход в панель и мониторинг

| Метод | Путь | Доступ и назначение |
| --- | --- | --- |
| GET | `/api/admin/status` | Без входа; только признак первоначальной настройки |
| POST | `/api/admin/setup` | Bearer-ключ, только до создания администратора; `{ "username":"owner", "password":"..." }` |
| POST | `/api/admin/login` | Имя и пароль, запрос с того же origin; устанавливает HttpOnly cookie |
| GET | `/api/admin/me` | Cookie администратора; имя текущего пользователя |
| POST | `/api/admin/logout` | Cookie и тот же origin; завершает вход в панель |
| GET | `/api/admin/monitor` | Cookie; состояние проверки Kaspi и признак настройки Telegram без токенов |
| POST | `/api/admin/notifications/test` | Cookie и тот же origin; тестовое сообщение в настроенный Telegram-чат |

Cookie действует 12 часов и сбрасывается при перезапуске сервера. Выход из панели не отключает Kaspi. POST-запросы с cookie защищены проверкой origin; серверные Bearer-запросы origin не требуют.

## Вебхуки

Создайте `STATE_DIR/webhooks.json` по [примеру](../webhooks.example.json). Для событий `payment.success`, `payment.failed`, `payment.expired`, `payment.lost` сервис отправляет POST с JSON-полями `event`, `paymentId`, `type`, `status`, `statusDesc`, `amount`, `data`, `timestamp`. Подпись в `X-Webhook-Signature` имеет формат `sha256=<hex HMAC-SHA256 от точного тела запроса>`; ключ — `secret` из конфигурации вебхука. Любой ответ вне диапазона 2xx приводит к повтору через 5 и 30 секунд, максимум три попытки.

Проверяйте подпись по исходным байтам тела и сравнивайте её постоянным по времени способом. Обрабатывайте повторные события идемпотентно по `event` + `paymentId`.
