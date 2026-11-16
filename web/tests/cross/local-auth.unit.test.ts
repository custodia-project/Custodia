import test from 'node:test';
import assert from 'node:assert/strict';
import { registerHooks } from 'node:module';

/* api.ts reads Vite's import.meta.env.VITE_API_URL, absent in Node. */
registerHooks({
  load(url, context, nextLoad) {
    const result = nextLoad(url, context);
    if (url.endsWith('/src/cross/lib/api.ts')) {
      const text = typeof result.source === 'string' ? result.source : Buffer.from(result.source as ArrayBuffer).toString('utf8');
      return { ...result, source: text.replaceAll('import.meta.env.VITE_API_URL', "''") };
    }
    return result;
  },
});

const { api, logout, setCSRFToken } = await import('../../src/cross/lib/api.ts');
const { createMockApi } = await import('../../src/demo/mock-api.ts');

function captureFetch(response: () => Response) {
  const original = globalThis.fetch;
  const calls: Array<{ method: string; headers: Headers }> = [];
  globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ method: init?.method ?? 'GET', headers: new Headers(init?.headers) });
    return response();
  }) as typeof fetch;
  return { calls, restore: () => { globalThis.fetch = original; } };
}

test('local mode mutations carry the in-memory CSRF token and reads do not', async () => {
  setCSRFToken('session-csrf-token');
  const { calls, restore } = captureFetch(() => new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }));
  try {
    await api('/workspaces/preferences', { method: 'PUT', body: '{}' });
    await api('/auth/status');
    await logout();
  } finally { restore(); setCSRFToken(''); }
  assert.equal(calls[0].headers.get('X-Custodia-CSRF'), 'session-csrf-token');
  assert.equal(calls[1].headers.get('X-Custodia-CSRF'), null);
  assert.equal(calls[2].method, 'POST');
  assert.equal(calls[2].headers.get('X-Custodia-CSRF'), 'session-csrf-token');
});

test('a 204 mutation resolves without inventing a JSON body', async () => {
  const { restore } = captureFetch(() => new Response(null, { status: 204 }));
  try {
    assert.equal(await api('/access/users/disable', { method: 'POST', body: '{}' }), undefined);
  } finally { restore(); }
});

async function call(handle: (request: { url: string; method: string; body: unknown }) => Promise<{ status: number; body: string }>, method: string, path: string, body?: unknown) {
  const response = await handle({ url: `https://custodia.example${path}`, method, body: body ?? null });
  return { status: response.status, data: response.body ? JSON.parse(response.body) as Record<string, unknown> : null };
}

test('the static demo exposes the local login, temporary password and revocable sessions', async () => {
  const handle = createMockApi({
    authenticated: false,
    authMode: 'local',
    permissions: ['access:local_users_manage'],
    localCredentials: { 'ada.admin': 'correct horse battery staple' },
  });
  assert.equal((await call(handle, 'GET', '/api/auth/status')).data?.authenticated, false);
  const wrong = await call(handle, 'POST', '/api/auth/login', { username: 'ada.admin', password: 'wrong password' });
  const unknown = await call(handle, 'POST', '/api/auth/login', { username: 'nobody', password: 'wrong password' });
  assert.equal(wrong.status, 401);
  assert.deepEqual(wrong.data, unknown.data);
  const login = await call(handle, 'POST', '/api/auth/login', { username: 'ada.admin', password: 'correct horse battery staple' });
  assert.equal(login.status, 200);
  assert.equal(typeof login.data?.csrf_token, 'string');
  assert.equal((await call(handle, 'GET', '/api/auth/status')).data?.authenticated, true);

  const created = await call(handle, 'POST', '/api/access/users', { username: 'bruno', display_name: 'Bruno Reader', password: 'temporary reader passphrase', connections: ['acme-identity'] });
  assert.equal(created.status, 201);
  assert.equal((await call(handle, 'POST', '/api/access/users', { username: 'bruno', display_name: 'Bruno', password: 'temporary reader passphrase', connections: [] })).status, 409);
  const list = await call(handle, 'GET', '/api/access/users');
  assert.equal((list.data?.rows as Array<Record<string, unknown>> | undefined)?.some((row) => row.username === 'bruno' && row.must_change_password === true), true);

  const reader = createMockApi({
    authenticated: false,
    permissions: ['access:local_users_manage'],
    localCredentials: { 'ada.admin': 'correct horse battery staple', bruno: 'temporary reader passphrase' },
    localUsers: [
      { username: 'ada.admin', display_name: 'Ada Admin', enabled: true, administrator: true, must_change_password: false, last_login_at: null, active_sessions: 0, connections: [] },
      { username: 'bruno', display_name: 'Bruno Reader', enabled: true, administrator: false, must_change_password: true, last_login_at: null, active_sessions: 1, connections: ['acme-identity'] },
    ],
  });
  const temporary = await call(reader, 'POST', '/api/auth/login', { username: 'bruno', password: 'temporary reader passphrase' });
  assert.equal(temporary.data?.must_change_password, true);
  const changed = await call(reader, 'POST', '/api/auth/password', { current_password: 'temporary reader passphrase', new_password: 'a brand new reader passphrase' });
  assert.equal(changed.status, 200);
  const afterChange = await call(reader, 'GET', '/api/auth/status');
  assert.equal((afterChange.data?.user as Record<string, unknown>).must_change_password, false);
  const revoked = await call(reader, 'POST', '/api/access/users/revoke-sessions', { username: 'bruno' });
  assert.equal(revoked.status, 204);
  const rows = (await call(reader, 'GET', '/api/access/users')).data?.rows as Array<Record<string, unknown>>;
  assert.equal(rows.find((row) => row.username === 'bruno')?.active_sessions, 0);
  assert.equal((await call(reader, 'POST', '/api/auth/logout')).data?.logged_out, true);
  assert.equal((await call(reader, 'GET', '/api/auth/status')).data?.authenticated, false);
});
