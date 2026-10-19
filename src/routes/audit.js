import { Router } from 'express';
import { listRequests } from '../audit.js';

const router = Router();
router.get('/requests', (req, res) => {
  const limit = Math.min(Math.max(Number.parseInt(req.query.limit, 10) || 50, 1), 200);
  const offset = Math.max(Number.parseInt(req.query.offset, 10) || 0, 0);
  res.json({ data: listRequests(req.serviceName, limit, offset) });
});
export default router;
