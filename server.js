import express from 'express';
import path from 'path';
import { PORT, HOST, ROOT_DIR } from './src/config.js';
import authRoutes from './src/routes/auth.js';
import invoiceRoutes from './src/routes/invoice.js';
import qrRoutes from './src/routes/qr.js';
import historyRoutes from './src/routes/history.js';
import refundRoutes from './src/routes/refund.js';
import sessionRoutes from './src/routes/session.js';
import auditRoutes from './src/routes/audit.js';
import { createServiceAuth, parseServiceKeys } from './src/serviceAuth.js';
import { initAudit } from './src/audit.js';
import { startPolling, stopPolling } from './src/polling.js';

const serviceKeys = parseServiceKeys(process.env.SERVICE_API_KEYS);
initAudit();
const app = express();

app.disable('x-powered-by');
app.use(express.json({ limit: '64kb' }));
app.use(express.static(path.join(ROOT_DIR, 'public')));

app.get('/health', (req, res) => res.json({ status: 'ok' }));
app.use('/api', createServiceAuth(serviceKeys));

app.use('/api/auth', authRoutes);
app.use('/api/invoice', invoiceRoutes);
app.use('/api/qr', qrRoutes);
app.use('/api/history', historyRoutes);
app.use('/api/refund', refundRoutes);
app.use('/api/session', sessionRoutes);
app.use('/api/audit', auditRoutes);

const server = app.listen(PORT, HOST, () => {
  console.log(`\n  🟢 Kaspi POS service running at http://${HOST}:${PORT}\n`);
  startPolling();
});

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => {
    stopPolling();
    server.close(() => process.exit(0));
  });
}
