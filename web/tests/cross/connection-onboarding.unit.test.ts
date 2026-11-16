import assert from 'node:assert/strict';
import test from 'node:test';
import {
  buildConnectionRequest, connectableConnectors, emptyConnectionForm,
  isAbsolutePath, isReference, splitScopes,
} from '../../src/cross/lib/connection-onboarding.ts';
import type { ConnectorDescriptor } from '../../src/cross/types/ingestion.ts';

const cyberArk: ConnectorDescriptor = {
  id: 'cyberark-pam-safes-v1',
  name: 'CyberArk PAM',
  vendor: 'CyberArk',
  category: 'PAM',
  status: 'available',
  auth_modes: ['bearer', 'oauth-client-credentials', 'mtls'],
  collections: ['safes', 'safe_members', 'accounts'],
  probe_endpoints: ['/API/Safes'],
  notes: 'Read-only PAM.',
};

function form(overrides: Partial<ReturnType<typeof emptyConnectionForm>> = {}) {
  return {
    ...emptyConnectionForm(cyberArk),
    baseUrl: 'https://pvwa.acme.example/PasswordVault',
    scope: 'pam-safes-prod',
    credentialRef: 'secret:pam-bearer',
    ...overrides,
  };
}

test('only implemented connectors are offered for onboarding', () => {
  const rows: ConnectorDescriptor[] = [
    cyberArk,
    { ...cyberArk, id: 'qradar', name: 'QRadar', status: 'planned', collections: [], auth_modes: [], probe_endpoints: [] },
    { ...cyberArk, id: 'http-inventory-v1', name: 'Lab', status: 'laboratory' },
  ];
  assert.deepEqual(connectableConnectors(rows).map((row) => row.id), ['cyberark-pam-safes-v1']);
});

test('a bearer tenant builds a request without ever sending the credential value', () => {
  const draft = buildConnectionRequest(form(), cyberArk);
  assert.equal(draft.ok, true);
  if (!draft.ok) return;
  assert.deepEqual(draft.request, {
    connector: 'cyberark-pam-safes-v1',
    base_url: 'https://pvwa.acme.example/PasswordVault',
    collections: ['safes'],
    scope: 'pam-safes-prod',
    auth_mode: 'bearer',
    credential_ref: 'secret:pam-bearer',
    interval_minutes: 0,
  });
  assert.equal(JSON.stringify(draft.request).includes('super-secret'), false);
});

test('one tenant connection may collect several collections with one credential', () => {
  const draft = buildConnectionRequest(form({ collections: ['safes', 'safe_members', 'accounts'] }), cyberArk);
  assert.equal(draft.ok, true);
  if (!draft.ok) return;
  assert.deepEqual(draft.request.collections, ['safes', 'safe_members', 'accounts']);
  assert.equal(draft.request.credential_ref, 'secret:pam-bearer');
  assert.equal(draft.request.base_url, 'https://pvwa.acme.example/PasswordVault');
});

test('an empty or unknown collection selection is rejected', () => {
  for (const collections of [[], ['safes', 'events']]) {
    const draft = buildConnectionRequest(form({ collections }), cyberArk);
    assert.equal(draft.ok, false);
    if (!draft.ok) assert.equal(draft.errors.some((message) => message.includes('colección')), true);
  }
});

test('oauth adds token endpoint, references and scopes', () => {
  const draft = buildConnectionRequest(form({
    authMode: 'oauth-client-credentials',
    credentialRef: '',
    tokenUrl: 'https://pvwa.acme.example/oauth2/platformtoken',
    clientIdRef: 'env:CUSTODIA_PAM_CLIENT_ID',
    clientSecretRef: 'secret:pam-client-secret',
    scopes: 'pam.read  pam.inventory',
    caFile: '/etc/custodia/acme-ca.pem',
  }), cyberArk);
  assert.equal(draft.ok, true);
  if (!draft.ok) return;
  assert.equal(draft.request.auth_mode, 'oauth-client-credentials');
  assert.equal(draft.request.token_url, 'https://pvwa.acme.example/oauth2/platformtoken');
  assert.equal(draft.request.client_id_ref, 'env:CUSTODIA_PAM_CLIENT_ID');
  assert.equal(draft.request.client_secret_ref, 'secret:pam-client-secret');
  assert.deepEqual(draft.request.scopes, ['pam.read', 'pam.inventory']);
  assert.equal(draft.request.ca_file, '/etc/custodia/acme-ca.pem');
  assert.equal('credential_ref' in draft.request, false);
});

test('mtls requires absolute certificate and key paths', () => {
  const missing = buildConnectionRequest(form({ authMode: 'mtls', credentialRef: '', clientCertFile: 'cliente.pem' }), cyberArk);
  assert.equal(missing.ok, false);
  const valid = buildConnectionRequest(form({
    authMode: 'mtls', credentialRef: '',
    clientCertFile: '/etc/custodia/cliente.pem', clientKeyFile: '/etc/custodia/cliente.key',
  }), cyberArk);
  assert.equal(valid.ok, true);
  if (valid.ok) assert.equal(valid.request.auth_mode, 'mtls');
});

test('unsafe URLs, literal credentials and bad intervals are rejected', () => {
  const cases = [
    [{ baseUrl: 'http://pvwa.acme.example' }, 'HTTPS'],
    [{ baseUrl: 'https://user:pass@pvwa.acme.example' }, 'credenciales'],
    [{ baseUrl: 'https://pvwa.acme.example?x=1' }, 'query'],
    [{ credentialRef: 'plain-token' }, 'referencia'],
    [{ scope: '  ' }, 'alcance'],
    [{ interval: '3' }, 'intervalo'],
    [{ interval: 'abc' }, 'intervalo'],
  ] as const;
  for (const [overrides, expected] of cases) {
    const draft = buildConnectionRequest(form(overrides), cyberArk);
    assert.equal(draft.ok, false, JSON.stringify(overrides));
    if (draft.ok) continue;
    assert.equal(draft.errors.some((message) => message.toLowerCase().includes(expected.toLowerCase())), true, draft.errors.join(' | '));
  }
});

test('a planned connector can never produce a request', () => {
  const planned: ConnectorDescriptor = { ...cyberArk, id: 'qradar', status: 'planned', collections: [], auth_modes: [] };
  const draft = buildConnectionRequest(form({ connector: 'qradar', collections: [] }), planned);
  assert.equal(draft.ok, false);
});

test('reference and scope helpers are strict', () => {
  assert.equal(isReference('env:TOKEN'), true);
  assert.equal(isReference('file:/run/secrets/token'), true);
  assert.equal(isReference('secret:pam'), true);
  assert.equal(isReference('env:'), false);
  assert.equal(isReference('super-secret'), false);
  assert.equal(isAbsolutePath('/etc/ca.pem'), true);
  assert.equal(isAbsolutePath('ca.pem'), false);
  assert.deepEqual(splitScopes('a,b  c'), ['a', 'b', 'c']);
});
