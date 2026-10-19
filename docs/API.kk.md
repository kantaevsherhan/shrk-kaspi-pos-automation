# Kaspi POS Console API

Негізгі адрес: `http://127.0.0.1:3000`. Барлық `/api/*` сұрауларына `Authorization: Bearer <SERVICE_API_KEY>` қажет. POST денесі JSON форматында. Толық маршруттар мен үлгілер [орыс тіліндегі API анықтамалығында](API.md) берілген.

Негізгі маршруттар: `POST /api/invoice/create` (шот), `POST /api/qr/create` (QR), `POST /api/history/operations` (Kaspi тарихы), `POST /api/refund/create` (қайтарым). `GET /api/auth/session` сессия күйін көрсетеді, `GET /health` кілтсіз жұмыс істейді. `GET /api/payments/active` тек аяқталмаған төлемдерді береді.

Вебхуктарды `STATE_DIR/webhooks.json` файлында орнатыңыз. Қолтаңба `X-Webhook-Signature: sha256=<HMAC-SHA256>` тақырыбында жіберіледі. SQLite журналы қолданылмайды.
