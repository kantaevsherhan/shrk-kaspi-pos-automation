import { Router } from 'express';
import { KASPI_QRPAY_URL } from '../config.js';
import { loggedFetch, signedQrPayHeaders } from '../helpers.js';
import { requireKaspiSession } from '../kaspiSession.js';
import { trackPayment } from '../polling.js';
import { recordRequest, updateOperationStatus } from '../audit.js';

const router = Router();

router.use(requireKaspiSession);

// ─── Create QR token ───

router.post('/create', async (req, res) => {
  const { amount, latitude, longitude } = req.body;
  if (!Number.isFinite(Number(amount)) || Number(amount) <= 0)
    return res.status(400).json({ error: 'Positive amount required' });

  try {
    const url = `${KASPI_QRPAY_URL}/v01/qr-token/create`;
    const payload = JSON.stringify({
      PaymentAmount: Number(amount),
      DeviceInterface: 'Pos',
      Latitude: latitude || 43.204643483375889,
      Longitude: longitude || 76.891962364115912,
    });
    const headers = { ...signedQrPayHeaders(url, req.session, payload), 'Content-Type': 'application/json' };
    const resp = await loggedFetch(url, {
      method: 'POST',
      headers,
      body: payload,
    });
    const kaspiResponse = await resp.json();
    const d = kaspiResponse.Data;
    recordRequest({ serviceName: req.serviceName, kind: 'qr', operationId: d?.QrOperationId,
      amount: Number(amount), status: d?.QrOperationId ? (d.Status || 'QrTokenCreated') : 'rejected',
      kaspiStatusCode: kaspiResponse.StatusCode,
      error: d?.QrOperationId ? null : kaspiResponse.Message || 'Kaspi did not create a QR token' });
    if (d?.QrOperationId && kaspiResponse.StatusCode === 0) {
      const opts = d.QrPaymentBehaviorOptions || {};
      trackPayment(
        d.QrOperationId,
        'qr',
        {
          tokenSN: req.session.tokenSN,
          vtokenSecret: req.session.vtokenSecret,
          profileId: req.session.profileId,
        },
        {
          qrToken: d.QrToken,
          expireDate: d.ExpireDate,
          receiptUrl: d.ReceiptUrl,
          amount: d.Amount,
          pollingIntervals: {
            scanWaitTimeout: Number(opts.qrCodeScanWaitTimeout) || 180,
            scanPollingInterval: Number(opts.qrCodeScanEventPollingInterval) || 3,
            statusCountdown: Number(opts.paymentStatusCountdown) || 2,
            confirmationTimeout: Number(opts.paymentConfirmationTimeout) || 65,
          },
        },
      );
    }
    if (d && d.QrToken) {
      d.QrOriginalToken = d.QrToken;
      d.QrToken = d.QrToken.replace('https://qr.kaspi.kz/', 'https://pay.kaspi.kz/pay/');
    }
    res.json(kaspiResponse);
  } catch (err) {
    recordRequest({ serviceName: req.serviceName, kind: 'qr', amount: Number(amount), status: 'error', error: err.message });
    res.status(500).json({ error: err.message });
  }
});

// ─── QR payment status ───

router.get('/status', async (req, res) => {
  const { qrOperationId } = req.query;
  if (!qrOperationId) return res.status(400).json({ error: 'qrOperationId required' });

  try {
    const url = `${KASPI_QRPAY_URL}/v02/kaspi-qr/status?qrOperationId=${qrOperationId}`;
    const resp = await loggedFetch(url, { headers: signedQrPayHeaders(url, req.session) });
    const body = await resp.json();
    if (body.Data?.Status) updateOperationStatus(qrOperationId, body.Data.Status);
    res.json(body);
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

export default router;
