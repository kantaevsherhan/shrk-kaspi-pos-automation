import { decryptSecret } from './crypto.js';
import { loadKaspiSession } from './audit.js';

export const requireKaspiSession = (req, res, next) => {
  try {
    const tokenSN = req.headers['x-token-sn'];
    const vtokenSecret = req.headers['x-vtoken-secret'];
    if (tokenSN || vtokenSecret) {
      if (!tokenSN || !vtokenSecret) return res.status(401).json({ error: 'Both Kaspi session headers are required.' });
      req.session = {
        tokenSN,
        vtokenSecret,
        profileId: req.headers['x-profile-id'] || null,
        decryptedSecret: decryptSecret(vtokenSecret),
      };
    } else {
      req.session = loadKaspiSession();
      if (!req.session) return res.status(401).json({ error: 'Kaspi account is not connected. Complete SMS login first.' });
    }
    next();
  } catch {
    return res.status(401).json({ error: 'Kaspi session is invalid. Sign in again.' });
  }
};
