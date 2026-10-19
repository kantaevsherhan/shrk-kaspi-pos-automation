import { Router } from 'express';
import { KASPI_QRPAY_URL } from '../config.js';
import { loggedFetch, signedQrPayHeaders } from '../helpers.js';
import { requireKaspiSession } from '../kaspiSession.js';
import { trackPayment } from '../polling.js';
import { recordRequest, updateOperationStatus } from '../audit.js';

const router = Router();

router.use(requireKaspiSession);

// ─── Client info ───

router.get('/client-info', async (req, res) => {
  const { phoneNumber } = req.query;
  if (!phoneNumber) return res.status(400).json({ error: 'phoneNumber required' });

  try {
    const url = `${KASPI_QRPAY_URL}/v01/remote/client-info?phoneNumber=${phoneNumber}`;
    const resp = await loggedFetch(url, { headers: signedQrPayHeaders(url, req.session) });
    res.json(await resp.json());
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

// ─── Create invoice ───

router.post('/create', async (req, res) => {
  const { phoneNumber, amount, comment } = req.body;
  if (!/^\d{10,11}$/.test(String(phoneNumber)) || !Number.isFinite(Number(amount)) || Number(amount) <= 0)
    return res.status(400).json({ error: 'Valid phoneNumber and positive amount required' });

  try {
    const url = `${KASPI_QRPAY_URL}/v01/remote/create`;
    const payload = JSON.stringify({ PhoneNumber: phoneNumber, Amount: Number(amount), Comment: comment || '' });
    const headers = { ...signedQrPayHeaders(url, req.session, payload), 'Content-Type': 'application/json' };
    const resp = await loggedFetch(url, {
      method: 'POST',
      headers,
      body: payload,
    });
    const kaspiResponse = await resp.json();
    const d = kaspiResponse.Data;
    const operationId = d?.QrOperationId ?? d?.Id;
    recordRequest({
      serviceName: req.serviceName, kind: 'invoice', operationId,
      recipientPhone: String(phoneNumber), amount: Number(amount), comment,
      status: operationId ? (d.Status || 'RemotePaymentCreated') : 'rejected',
      kaspiStatusCode: kaspiResponse.StatusCode,
      error: operationId ? null : kaspiResponse.Message || 'Kaspi did not create an invoice',
    });
    if (operationId && kaspiResponse.StatusCode === 0) {
      trackPayment(
        operationId,
        'invoice',
        {
          tokenSN: req.session.tokenSN,
          vtokenSecret: req.session.vtokenSecret,
          profileId: req.session.profileId,
        },
        {
          amount: d.Amount,
          clientMobile: d.ClientMobile,
          receiptUrl: d.ReceiptUrl,
          orderNumber: d.OrderNumber,
        },
      );
    }
    res.json(kaspiResponse);
  } catch (err) {
    recordRequest({ serviceName: req.serviceName, kind: 'invoice', recipientPhone: String(phoneNumber), amount: Number(amount), comment, status: 'error', error: err.message });
    res.status(500).json({ error: err.message });
  }
});

// ─── Invoice details ───

router.get('/details', async (req, res) => {
  const { operationId } = req.query;
  if (!operationId) return res.status(400).json({ error: 'operationId required' });

  try {
    const url = `${KASPI_QRPAY_URL}/v02/remote/details?operationId=${operationId}`;
    const resp = await loggedFetch(url, { headers: signedQrPayHeaders(url, req.session) });
    const body = await resp.json();
    if (body.Data?.Status) updateOperationStatus(operationId, body.Data.Status);
    res.json(body);
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

// ─── Cancel invoice ───

router.post('/cancel', async (req, res) => {
  const { operationId } = req.body;
  if (!operationId) return res.status(400).json({ error: 'operationId required' });

  try {
    const url = `${KASPI_QRPAY_URL}/v01/remote/cancel`;
    const payload = JSON.stringify({ qrOperationId: Number(operationId) });
    const headers = { ...signedQrPayHeaders(url, req.session, payload), 'Content-Type': 'application/json' };
    const resp = await loggedFetch(url, {
      method: 'POST',
      headers,
      body: payload,
    });
    const body = await resp.json();
    recordRequest({ serviceName: req.serviceName, kind: 'cancel', operationId, status: body.StatusCode === 0 ? 'accepted' : 'rejected', kaspiStatusCode: body.StatusCode, error: body.StatusCode === 0 ? null : body.Message });
    res.json(body);
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

// ─── Invoice history ───

router.post('/history', async (req, res) => {
  try {
    const url = `${KASPI_QRPAY_URL}/v01/remote/history`;
    const payload = JSON.stringify({ MaxResult: 20 });
    const headers = { ...signedQrPayHeaders(url, req.session, payload), 'Content-Type': 'application/json' };
    const resp = await loggedFetch(url, {
      method: 'POST',
      headers,
      body: payload,
    });
    res.json(await resp.json());
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

export default router;
