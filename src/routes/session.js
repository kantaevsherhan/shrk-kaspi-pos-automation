import { Router } from 'express';
import { KASPI_QRPAY_URL } from '../config.js';
import { loggedFetch, signedQrPayHeaders } from '../helpers.js';
import { requireKaspiSession } from '../kaspiSession.js';

const router = Router();

// ─── Check session validity ───

router.get('/check', requireKaspiSession, async (req, res) => {
  const session = req.session;
  try {
    const url = `${KASPI_QRPAY_URL}/v02/history/operations`;
    const payload = JSON.stringify({
      EndDate: new Date().toISOString().slice(0, 10),
      LastTransactionDate: '',
      StatementPeriodCode: 0,
    });
    const headers = { ...signedQrPayHeaders(url, session, payload), 'Content-Type': 'application/json' };
    const resp = await loggedFetch(url, {
      method: 'POST',
      headers,
      body: payload,
    });

    const body = await resp.json().catch(() => ({}));

    // Kaspi may return HTTP 200 but with error StatusCode in body
    if (resp.ok && (!body.StatusCode || body.StatusCode === 0)) {
      return res.json({ active: true, orgName: session.orgName || null, phone: session.phone || null });
    }

    return res.status(resp.ok ? 401 : resp.status).json({
      active: false,
      error: body.Message || body.message || 'Session rejected by Kaspi API.',
      code: body.StatusCode || body.Code,
      details: body,
    });
  } catch (err) {
    return res.status(500).json({ active: false, error: err.message });
  }
});

export default router;
