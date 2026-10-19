import crypto from 'crypto';

export const parseServiceKeys = (raw) => {
  if (!raw) throw new Error('SERVICE_API_KEYS is required (JSON object: {"service-name":"random-secret"}).');
  let keys;
  try {
    keys = JSON.parse(raw);
  } catch {
    throw new Error('SERVICE_API_KEYS must be a JSON object.');
  }
  if (!keys || Array.isArray(keys) || typeof keys !== 'object' || !Object.keys(keys).length) {
    throw new Error('SERVICE_API_KEYS must contain at least one service.');
  }
  for (const [name, secret] of Object.entries(keys)) {
    if (!/^[a-zA-Z0-9_-]{1,64}$/.test(name) || typeof secret !== 'string' || secret.length < 32 || secret.startsWith('replace-')) {
      throw new Error(`Invalid SERVICE_API_KEYS entry: ${name}. Use a random secret of at least 32 characters.`);
    }
  }
  if (new Set(Object.values(keys)).size !== Object.keys(keys).length)
    throw new Error('SERVICE_API_KEYS secrets must be unique per service.');
  return keys;
};

export const createServiceAuth = (keys) => (req, res, next) => {
  const match = /^Bearer ([^\s]+)$/i.exec(req.get('authorization') || '');
  if (!match) return res.status(401).json({ error: 'Bearer token required.' });
  const supplied = crypto.createHash('sha256').update(match[1]).digest();
  for (const [name, secret] of Object.entries(keys)) {
    const expected = crypto.createHash('sha256').update(secret).digest();
    if (crypto.timingSafeEqual(supplied, expected)) {
      req.serviceName = name;
      return next();
    }
  }
  return res.status(401).json({ error: 'Invalid bearer token.' });
};
