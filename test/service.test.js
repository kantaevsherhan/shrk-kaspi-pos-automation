import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const temp = fs.mkdtempSync(path.join(os.tmpdir(), 'kaspi-service-test-'));
process.env.STATE_DIR = temp;
process.env.TOKEN_SECRET_KEY = 'a'.repeat(64);

const { parseServiceKeys, createServiceAuth } = await import('../src/serviceAuth.js');
const { initAudit, recordRequest, listRequests, saveKaspiSession, loadKaspiSession, clearKaspiSession, updateOperationStatus } = await import('../src/audit.js');

test('service keys reject missing or weak configuration and isolate callers', () => {
  assert.throws(() => parseServiceKeys(''), /required/);
  assert.throws(() => parseServiceKeys('{"a":"weak"}'), /Invalid/);
  const auth = createServiceAuth({ a: 'a'.repeat(32), b: 'b'.repeat(32) });
  const response = () => ({ status(code) { this.code = code; return this; }, json(body) { this.body = body; return this; } });
  const rejected = response();
  auth({ get: () => 'Bearer wrong' }, rejected, () => assert.fail('must reject'));
  assert.equal(rejected.code, 401);
  const req = { get: () => `Bearer ${'b'.repeat(32)}` };
  auth(req, response(), () => assert.equal(req.serviceName, 'b'));
});

test('SQLite records caller, recipient and status', () => {
  initAudit();
  recordRequest({ serviceName: 'a', kind: 'invoice', operationId: '123', recipientPhone: '77000000000', amount: 100, status: 'RemotePaymentCreated' });
  updateOperationStatus('123', 'Processed');
  assert.equal(listRequests('a')[0].status, 'Processed');
  assert.equal(listRequests('a')[0].recipient_phone, '77000000000');
  assert.equal(listRequests('b').length, 0);
});

// Keep session encryption verification in a separate test to avoid exposing raw secrets in output.
test('stored Kaspi session can be restored and cleared', async () => {
  const { encryptSecret } = await import('../src/crypto.js');
  saveKaspiSession({ tokenSN: 'private-token', vtokenSecret: encryptSecret(Buffer.from('private-secret')), orgName: 'Shop' });
  assert.equal(loadKaspiSession().tokenSN, 'private-token');
  assert.equal(loadKaspiSession().decryptedSecret.toString(), 'private-secret');
  clearKaspiSession();
  assert.equal(loadKaspiSession(), null);
});
