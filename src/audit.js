import fs from 'fs';
import path from 'path';
import { DatabaseSync } from 'node:sqlite';
import { STATE_DIR } from './config.js';
import { encryptSecret, decryptSecret } from './crypto.js';

let db;

export const initAudit = (filename = process.env.AUDIT_DB_PATH || path.join(STATE_DIR, 'audit.sqlite')) => {
  if (db) return db;
  fs.mkdirSync(path.dirname(filename), { recursive: true, mode: 0o700 });
  db = new DatabaseSync(filename);
  fs.chmodSync(filename, 0o600);
  db.exec(`
    PRAGMA journal_mode = WAL;
    CREATE TABLE IF NOT EXISTS payment_requests (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      created_at TEXT NOT NULL DEFAULT (datetime('now')),
      updated_at TEXT NOT NULL DEFAULT (datetime('now')),
      service_name TEXT NOT NULL,
      kind TEXT NOT NULL CHECK (kind IN ('invoice', 'qr', 'refund', 'cancel')),
      operation_id TEXT,
      recipient_phone TEXT,
      amount REAL,
      comment TEXT,
      status TEXT NOT NULL,
      kaspi_status_code INTEGER,
      error TEXT
    );
    CREATE INDEX IF NOT EXISTS payment_requests_service_date
      ON payment_requests(service_name, created_at DESC);
    CREATE INDEX IF NOT EXISTS payment_requests_operation
      ON payment_requests(operation_id);
    CREATE TABLE IF NOT EXISTS kaspi_session (
      id INTEGER PRIMARY KEY CHECK (id = 1),
      token_sn TEXT NOT NULL,
      vtoken_secret TEXT NOT NULL,
      profile_id TEXT,
      org_name TEXT,
      phone TEXT,
      updated_at TEXT NOT NULL DEFAULT (datetime('now'))
    );
  `);
  return db;
};

export const recordRequest = (row) => {
  const database = initAudit();
  return database.prepare(`INSERT INTO payment_requests
    (service_name, kind, operation_id, recipient_phone, amount, comment, status, kaspi_status_code, error)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`).run(
    row.serviceName, row.kind, row.operationId == null ? null : String(row.operationId),
    row.recipientPhone || null, row.amount ?? null, row.comment || null,
    row.status, row.kaspiStatusCode ?? null, row.error || null,
  );
};

export const updateOperationStatus = (operationId, status) => {
  if (operationId == null || !status) return;
  initAudit().prepare(`UPDATE payment_requests SET status = ?, updated_at = datetime('now')
    WHERE operation_id = ? AND kind IN ('invoice', 'qr')`).run(status, String(operationId));
};

export const listRequests = (serviceName, limit = 50, offset = 0) =>
  initAudit().prepare(`SELECT id, created_at, updated_at, service_name, kind, operation_id,
    recipient_phone, amount, comment, status, kaspi_status_code, error
    FROM payment_requests WHERE service_name = ? ORDER BY id DESC LIMIT ? OFFSET ?`)
    .all(serviceName, limit, offset);

export const saveKaspiSession = (session) => {
  if (!session.tokenSN || !session.vtokenSecret) throw new Error('Kaspi login did not return a usable session.');
  initAudit().prepare(`INSERT INTO kaspi_session (id, token_sn, vtoken_secret, profile_id, org_name, phone)
    VALUES (1, ?, ?, ?, ?, ?)
    ON CONFLICT(id) DO UPDATE SET token_sn=excluded.token_sn, vtoken_secret=excluded.vtoken_secret,
    profile_id=excluded.profile_id, org_name=excluded.org_name, phone=excluded.phone, updated_at=datetime('now')`)
    .run(encryptSecret(Buffer.from(session.tokenSN)).toString(), session.vtokenSecret,
      session.profileId == null ? null : String(session.profileId), session.orgName || null, session.phone || null);
};

export const loadKaspiSession = () => {
  const row = initAudit().prepare('SELECT * FROM kaspi_session WHERE id = 1').get();
  if (!row) return null;
  return {
    tokenSN: decryptSecret(row.token_sn).toString(),
    vtokenSecret: row.vtoken_secret,
    decryptedSecret: decryptSecret(row.vtoken_secret),
    profileId: row.profile_id,
    orgName: row.org_name,
    phone: row.phone,
  };
};

export const clearKaspiSession = () => initAudit().prepare('DELETE FROM kaspi_session WHERE id = 1').run();
