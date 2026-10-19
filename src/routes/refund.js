import { Router } from 'express';
import { KASPI_QRPAY_URL } from '../config.js';
import { loggedFetch, signedQrPayHeaders } from '../helpers.js';
import { requireKaspiSession } from '../kaspiSession.js';
import { recordRequest } from '../audit.js';

const router = Router();

router.use(requireKaspiSession);

// ─── Return (refund) ───

router.post('/create', async (req, res) => {
  const { qrOperationId, returnAmount } = req.body;
  if (!/^\d+$/.test(String(qrOperationId)) || !Number.isFinite(Number(returnAmount)) || Number(returnAmount) <= 0)
    return res.status(400).json({ error: 'Valid qrOperationId and positive returnAmount required' });
  try {
    const url = `${KASPI_QRPAY_URL}/v01/kaspi-qr/history-pos-return`;
    const payload = JSON.stringify({
      ReturnAmount: Number(returnAmount),
      QrOperationId: Number(qrOperationId),
      DeviceInterface: 'Pos',
    });
    const headers = { ...signedQrPayHeaders(url, req.session, payload), 'Content-Type': 'application/json' };
    const resp = await loggedFetch(url, {
      method: 'POST',
      headers,
      body: payload,
    });
    const body = await resp.json();
    recordRequest({ serviceName: req.serviceName, kind: 'refund', operationId: qrOperationId,
      amount: Number(returnAmount), status: body.StatusCode === 0 ? 'accepted' : 'rejected',
      kaspiStatusCode: body.StatusCode, error: body.StatusCode === 0 ? null : body.Message });
    res.json(body);
  } catch (err) {
    recordRequest({ serviceName: req.serviceName, kind: 'refund', operationId: qrOperationId,
      amount: Number(returnAmount), status: 'error', error: err.message });
    res.status(500).json({ error: err.message });
  }
});

export default router;
