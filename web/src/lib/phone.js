export function nationalPhone(value) {
  const digits = String(value ?? '').replace(/\D/g, '');
  return digits.length === 11 && /^[78]/.test(digits) ? digits.slice(1) : digits;
}

export function isValidPhone(value) {
  return /^\d{10}$/.test(nationalPhone(value));
}

export function normalizePhone(value) {
  return isValidPhone(value) ? `7${nationalPhone(value)}` : '';
}

export function parsePhoneInput(value) {
  const text = String(value ?? '');
  const digits = text.replace(/\D/g, '');
  return /^\s*\+7/.test(text) ? digits.slice(1) : nationalPhone(text);
}

export function formatPhoneInput(value) {
  const digits = nationalPhone(value);
  return digits ? `+7 ${formatNationalPhone(digits)}` : '';
}

export function formatNationalPhone(value) {
  const digits = nationalPhone(value);
  if (!digits || digits.length > 10) return digits;
  let result = `(${digits.slice(0, 3)}`;
  if (digits.length >= 3) result += ')';
  if (digits.length > 3) result += ` ${digits.slice(3, 6)}`;
  if (digits.length > 6) result += `-${digits.slice(6, 8)}`;
  if (digits.length > 8) result += `-${digits.slice(8, 10)}`;
  return result;
}

export function formatPhone(value) {
  if (!value) return '—';
  return isValidPhone(value) ? `+7 ${formatNationalPhone(value)}` : String(value);
}
