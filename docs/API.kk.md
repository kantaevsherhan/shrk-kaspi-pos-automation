# Kaspi POS Console API

Негізгі адрес: `http://127.0.0.1:3000`. Сервистер төлем API-іне `Authorization: Bearer <SERVICE_API_KEY>` арқылы қосылады, панель әкімшінің cookie-сессиясын қолданады. POST денесі JSON форматында. Толық маршруттар, құпиясөз арқылы кіру және Telegram мониторингі [орыс тіліндегі API анықтамалығында](API.md) берілген.

Негізгі маршруттар: `POST /api/invoice/create` (шот), `POST /api/qr/create` (QR), `POST /api/history/operations` (Kaspi тарихы), `POST /api/refund/create` (қайтарым). `GET /api/auth/session` сессия күйін көрсетеді, `GET /health` кілтсіз жұмыс істейді. `GET /api/payments/active` тек аяқталмаған төлемдерді береді.

Вебхуктарды `STATE_DIR/webhooks.json` файлында орнатыңыз. Қолтаңба `X-Webhook-Signature: sha256=<HMAC-SHA256>` тақырыбында жіберіледі. SQLite журналы қолданылмайды.
