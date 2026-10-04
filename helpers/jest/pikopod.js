'use strict';

const crypto = require('node:crypto');

const DEFAULT_URL = process.env.PIKOPOD_URL || 'http://127.0.0.1:4600';
const MAX_SCOPE = 128;

function testScope() {
  const state = globalThis.expect && typeof globalThis.expect.getState === 'function' ? globalThis.expect.getState() : null;
  const name = state && state.currentTestName;
  if (!name) throw new Error('scoped() needs a scope: pass one, or call it inside a Jest test');
  const scope = `${name}:${process.env.JEST_WORKER_ID || ''}`;
  return scope.length <= MAX_SCOPE ? scope : crypto.createHash('sha256').update(scope).digest('hex');
}

class PikopodError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

class Pikopod {
  constructor({ baseUrl = DEFAULT_URL, sandbox, token = process.env.PIKOPOD_TOKEN, credential, scope } = {}) {
    if (!sandbox) throw new Error('Pikopod needs a sandbox name');
    this.baseUrl = baseUrl.replace(/\/$/, '');
    this.sandbox = sandbox;
    this.token = token;
    this.credential = credential;
    this.scope = scope;
  }

  scoped(scope = testScope()) {
    const child = new Pikopod({ baseUrl: this.baseUrl, sandbox: this.sandbox, token: this.token, credential: this.credential, scope });
    child.parent = this.parent;
    return child;
  }

  scopeQuery(action) {
    if (!this.scope) return action;
    return `${action}${action.includes('?') ? '&' : '?'}scope=${encodeURIComponent(this.scope)}`;
  }

  url() {
    return `${this.baseUrl}/${this.sandbox}`;
  }

  headers(extra = {}) {
    const h = { 'content-type': 'application/json', ...extra };
    if (this.token) h['x-pikopod-token'] = this.token;
    if (this.scope) h['x-pikopod-scope'] = this.scope;
    if (this.credential && !h.authorization) h.authorization = `Bearer ${this.credential}`;
    return h;
  }

  async call(method, action, body) {
    const res = await fetch(`${this.baseUrl}/_pikopod/v1/sandboxes/${this.sandbox}/${action}`, {
      method,
      headers: this.headers(),
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await res.text();
    let data = {};
    try { data = text ? JSON.parse(text) : {}; } catch { data = { message: text }; }
    if (res.status >= 400) throw new PikopodError(res.status, data.message || `${method} ${action}: ${res.status}`);
    return data;
  }

  async fork() {
    const f = await this.call('POST', 'fork');
    const child = new Pikopod({ baseUrl: this.baseUrl, sandbox: f.name, token: this.token, credential: f.credential });
    child.parent = this;
    return child;
  }

  async delete() {
    if (!this.parent) throw new Error('only a fork can be deleted');
    return this.call('DELETE', 'fork');
  }

  async mode(name, bind = {}) {
    return (await this.call('POST', 'mode', { name, bind })).mode;
  }

  async verify() {
    const { result } = await this.call('POST', this.scopeQuery('mode/verify'));
    return { ...result, passed: result.status === 'PASSED' };
  }

  async clearMode() {
    return this.call('DELETE', 'mode');
  }

  async chaos(rule) {
    return (await this.call('POST', 'faults', { probability: 1, ...rule })).armed;
  }

  async clearFaults(method = '', path = '') {
    const q = new URLSearchParams({ method, path }).toString();
    return (await this.call('DELETE', `faults?${q}`)).cleared;
  }

  async emit(event, data) {
    return this.call('POST', 'webhooks/emit', { event, data });
  }

  async requests(limit = 0) {
    return (await this.call('GET', this.scopeQuery(limit ? `requests?limit=${limit}` : 'requests'))).requests || [];
  }

  async seed(items) {
    return this.call('POST', 'seed', items);
  }

  async snapshot() {
    return (await this.call('POST', 'snapshot')).token;
  }

  async restore(token) {
    return this.call('POST', 'restore', { token });
  }

  async reset() {
    return this.call('POST', 'reset');
  }

  async fetch(path, init = {}) {
    return fetch(`${this.url()}${path}`, { ...init, headers: this.headers(init.headers || {}) });
  }
}

module.exports = { Pikopod, PikopodError, testScope };
