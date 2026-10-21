import test from 'node:test';
import assert from 'node:assert/strict';
import {
  formatNationalPhone,
  formatPhone,
  formatPhoneInput,
  isValidPhone,
  nationalPhone,
  normalizePhone,
  parsePhoneInput,
} from './phone.js';

test('full and national pasted phones identify the same recipient', () => {
  for (const value of ['7001234567', '77001234567', '+77001234567', '+7 (700) 123-45-67', '8 (700) 123-45-67']) {
    assert.equal(nationalPhone(value), '7001234567');
    assert.equal(normalizePhone(value), '77001234567');
    assert.equal(formatPhone(value), '+7 (700) 123-45-67');
    assert.equal(isValidPhone(value), true);
  }
});

test('country code is part of the editable value without growing while typing', () => {
  assert.equal(formatPhoneInput('7001234567'), '+7 (700) 123-45-67');
  assert.equal(parsePhoneInput('+7 (700) 123-45-67'), '7001234567');
  assert.equal(parsePhoneInput('+7 (70'), '70');
  assert.equal(parsePhoneInput('+7 '), '');
  assert.equal(formatPhoneInput(parsePhoneInput('+7 (700) 123-45-6')), '+7 (700) 123-45-6');
  for (const value of ['7001234567', '77001234567', '87001234567', '+7 (700) 123-45-67']) {
    assert.equal(formatPhoneInput(parsePhoneInput(value)), '+7 (700) 123-45-67');
  }
});

test('incomplete or overlong phone cannot silently become another recipient', () => {
  for (const value of ['', '700123456', '97001234567', '770012345678']) {
    assert.equal(isValidPhone(value), false);
    assert.equal(normalizePhone(value), '');
  }
  assert.equal(nationalPhone('770012345678'), '770012345678');
});

test('mask formats partial typing and display consistently', () => {
  assert.equal(formatNationalPhone(''), '');
  assert.equal(formatNationalPhone('70'), '(70');
  assert.equal(formatNationalPhone('700'), '(700)');
  assert.equal(formatNationalPhone('7001234'), '(700) 123-4');
  assert.equal(formatNationalPhone('7001234567'), '(700) 123-45-67');
  assert.equal(formatPhone(null), '—');
});
