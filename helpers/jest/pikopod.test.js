'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { Pikopod } = require('./pikopod');

const SANDBOX = process.env.PIKOPOD_SANDBOX || 'examplepay';

async function createCharge(sbx, attempts) {
  let status = 0;
  for (let i = 0; i < attempts; i++) {
    const res = await sbx.fetch('/charges', {
      method: 'POST',
      headers: { 'idempotency-key': 'order-77' },
      body: JSON.stringify({ amount: 5000, currency: 'usd' }),
    });
    await res.text();
    status = res.status;
    if (status < 500) return status;
  }
  return status;
}

test('a client that retries survives retry_storm inside a fork', async () => {
  const fork = await new Pikopod({ sandbox: SANDBOX }).fork();
  try {
    await fork.mode('retry_storm');
    assert.equal(await createCharge(fork, 5), 201);
    const result = await fork.verify();
    assert.equal(result.passed, true, result.summary);
    assert.equal((await fork.requests()).length, 3);
  } finally {
    await fork.delete();
  }
});

test('a client that gives up fails verify', async () => {
  const fork = await new Pikopod({ sandbox: SANDBOX }).fork();
  try {
    await fork.mode('retry_storm');
    assert.equal(await createCharge(fork, 1), 503);
    const result = await fork.verify();
    assert.equal(result.passed, false);
    assert.match(result.summary, /never matched/);
  } finally {
    await fork.delete();
  }
});

test('seed, chaos and reset', async () => {
  const fork = await new Pikopod({ sandbox: SANDBOX }).fork();
  try {
    await fork.seed({ charges: [{ id: 'ch_1', amount: 100, currency: 'usd', status: 'success' }] });
    assert.equal((await fork.fetch('/charges/ch_1')).status, 200);
    await fork.chaos({ kind: 'error', status: 503, method: 'GET', path: '/charges/{id}' });
    assert.equal((await fork.fetch('/charges/ch_1')).status, 503);
    await fork.reset();
    assert.equal((await fork.requests()).length, 0);
    assert.equal((await fork.fetch('/charges/ch_1')).status, 200);
  } finally {
    await fork.delete();
  }
});
