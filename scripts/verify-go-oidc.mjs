// OIDC mode end to end in Chromium against the real Go server, SQLite and the
// real web. A local fake provider publishes discovery, JWKS and a token
// endpoint; the administrator configures it from the console, an unknown
// identity waits for approval, and the same identity then signs in.
import { spawn, execFileSync } from 'node:child_process';
import { createHash, generateKeyPairSync, randomBytes, sign } from 'node:crypto';
import { mkdtemp, rm } from 'node:fs/promises';
import { createServer as createHTTPServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer as createNetServer } from 'node:net';
import { chromium, expect } from '../web/node_modules/@playwright/test/index.mjs';

const root = resolve(import.meta.dirname, '..');
const temporary = await mkdtemp(join(tmpdir(), 'custodia-oidc-'));
const binary = join(temporary, 'custodia');
const data = join(temporary, 'data');
const webBuild = join(temporary, 'web');
const adminPassword = 'oidc browser passphrase 16';
const clientId = 'custodia-browser';
const subject = 'idp-ada-subject';
let server, browser, provider;

async function freePort() {
  const probe = createNetServer();
  await new Promise((done) => probe.listen(0, '127.0.0.1', done));
  const port = probe.address().port;
  await new Promise((done) => probe.close(done));
  return port;
}

const b64 = (value) => Buffer.from(value).toString('base64url');
const { privateKey, publicKey } = generateKeyPairSync('rsa', { modulusLength: 2048 });
const jwk = publicKey.export({ format: 'jwk' });
const codes = new Map();

function idToken(nonce) {
  const now = Math.floor(Date.now() / 1000);
  const header = b64(JSON.stringify({ alg: 'RS256', typ: 'JWT', kid: 'browser-key' }));
  const claims = b64(JSON.stringify({
    iss: providerIssuer, aud: [clientId], sub: subject,
    exp: now + 300, iat: now - 5, nonce,
    email: 'ada.external@acme.example', name: 'Ada Externa',
  }));
  const signature = sign('RSA-SHA256', Buffer.from(`${header}.${claims}`), privateKey).toString('base64url');
  return `${header}.${claims}.${signature}`;
}

let providerIssuer = '';

try {
  execFileSync('npm', ['--prefix', 'web', 'run', 'build', '--', '--outDir', webBuild], { cwd: root, env: { ...process.env, VITE_DEMO: '0' }, stdio: 'inherit' });
  execFileSync('go', ['build', '-o', binary, './cmd/custodia'], { cwd: join(root, 'server'), env: { ...process.env, CGO_ENABLED: '0' }, stdio: 'inherit' });
  execFileSync(binary, ['init-lab', '--dir', data], { stdio: 'pipe' });
  execFileSync(binary, ['create-admin', '--dir', data, '--username', 'ada.admin', '--display-name', 'Ada Admin', '--connections', 'acme-identity,acme-netskope'], { input: `${adminPassword}\n`, stdio: ['pipe', 'pipe', 'pipe'] });

  const providerPort = await freePort();
  providerIssuer = `http://127.0.0.1:${providerPort}`;
  provider = createHTTPServer(async (request, response) => {
    const url = new URL(request.url, providerIssuer);
    if (url.pathname === '/.well-known/openid-configuration') {
      response.setHeader('Content-Type', 'application/json');
      response.end(JSON.stringify({
        issuer: providerIssuer,
        authorization_endpoint: `${providerIssuer}/authorize`,
        token_endpoint: `${providerIssuer}/token`,
        jwks_uri: `${providerIssuer}/keys`,
        response_types_supported: ['code'],
      }));
      return;
    }
    if (url.pathname === '/keys') {
      response.setHeader('Content-Type', 'application/json');
      response.end(JSON.stringify({ keys: [{ kty: 'RSA', kid: 'browser-key', use: 'sig', alg: 'RS256', n: jwk.n, e: jwk.e }] }));
      return;
    }
    if (url.pathname === '/authorize') {
      const query = url.searchParams;
      if (query.get('response_type') !== 'code' || query.get('client_id') !== clientId || query.get('code_challenge_method') !== 'S256') {
        response.writeHead(400).end('bad authorization request');
        return;
      }
      const code = randomBytes(18).toString('base64url');
      codes.set(code, { nonce: query.get('nonce'), challenge: query.get('code_challenge'), used: false });
      const target = new URL(query.get('redirect_uri'));
      target.searchParams.set('code', code);
      target.searchParams.set('state', query.get('state'));
      response.writeHead(303, { Location: target.toString() }).end();
      return;
    }
    if (url.pathname === '/token' && request.method === 'POST') {
      let body = '';
      for await (const chunk of request) body += chunk;
      const form = new URLSearchParams(body);
      const entry = codes.get(form.get('code'));
      const verifier = form.get('code_verifier') ?? '';
      const challenge = createHash('sha256').update(verifier).digest('base64url');
      if (!entry || entry.used || form.get('grant_type') !== 'authorization_code' || challenge !== entry.challenge) {
        response.writeHead(400, { 'Content-Type': 'application/json' }).end(JSON.stringify({ error: 'invalid_grant' }));
        return;
      }
      entry.used = true;
      response.setHeader('Content-Type', 'application/json');
      response.end(JSON.stringify({ access_token: 'opaque-access-token', token_type: 'Bearer', expires_in: 300, id_token: idToken(entry.nonce) }));
      return;
    }
    response.writeHead(404).end();
  });
  await new Promise((done) => provider.listen(providerPort, '127.0.0.1', done));

  const port = await freePort();
  const origin = `http://127.0.0.1:${port}`;
  server = spawn(binary, ['serve', '--auth-mode', 'oidc', '--public-origin', origin, '--dir', data, '--web-dir', webBuild, '--listen', `127.0.0.1:${port}`], { stdio: ['ignore', 'pipe', 'pipe'] });
  let failure = '';
  server.stderr.on('data', (chunk) => { failure += chunk; });
  await new Promise((done, fail) => {
    const timer = setTimeout(() => fail(Error('OIDC server startup timed out')), 10_000);
    server.stdout.once('data', () => { clearTimeout(timer); done(); });
    server.once('exit', (code) => { clearTimeout(timer); fail(Error(`OIDC server exited ${code}: ${failure}`)); });
  });

  browser = await chromium.launch();
  const admin = await (await browser.newContext({ locale: 'es-AR' })).newPage();
  await admin.goto(origin + '/');
  await expect(admin.getByRole('heading', { name: 'Iniciar sesión' })).toBeVisible();
  await expect(admin.getByRole('link', { name: 'Continuar con el proveedor' })).toHaveCount(0);
  await admin.getByRole('textbox', { name: 'Usuario', exact: true }).fill('ada.admin');
  await admin.locator('input[type="password"]').fill(adminPassword);
  await admin.getByRole('button', { name: 'Iniciar sesión' }).click();
  await expect(admin.getByRole('button', { name: 'Abrir menú de usuario' })).toBeVisible();

  await admin.goto(origin + '/users');
  await admin.getByRole('tab', { name: /Acceso OIDC/ }).click();
  await expect(admin.getByText('Sin configurar', { exact: true })).toBeVisible();
  await admin.getByLabel('Emisor (issuer)', { exact: true }).fill(providerIssuer);
  await admin.getByLabel('Client ID', { exact: true }).fill(clientId);
  await admin.getByRole('button', { name: 'Guardar borrador' }).click();
  await expect(admin.getByText('Borrador', { exact: true })).toBeVisible();
  await admin.getByRole('button', { name: 'Probar configuración' }).click();
  await expect(admin.getByText(/Descubrimiento y claves verificados/)).toBeVisible();
  await admin.getByRole('button', { name: 'Activar proveedor' }).click();
  await expect(admin.getByText('Activo', { exact: true }).first()).toBeVisible();

  // A valid but unknown identity waits for approval; it gets no session.
  const external = await (await browser.newContext({ locale: 'es-AR' })).newPage();
  await external.goto(origin + '/');
  await external.getByRole('link', { name: 'Continuar con el proveedor' }).click();
  await expect(external.getByText(/Tu identidad quedó pendiente de aprobación/)).toBeVisible();
  await expect(external.getByRole('textbox', { name: 'Usuario', exact: true })).toBeVisible();

  // The administrator approves it with only one connection.
  await admin.reload();
  await admin.getByRole('tab', { name: /Acceso OIDC/ }).click();
  const pendingRow = admin.getByRole('row', { name: /Ada Externa/ });
  await expect(pendingRow).toBeVisible();
  await pendingRow.getByRole('button', { name: 'Aprobar' }).click();
  const dialog = admin.getByRole('dialog', { name: 'Aprobar identidad' });
  await expect(dialog).toContainText(subject);
  await dialog.getByRole('checkbox', { name: 'acme-identity' }).check();
  await dialog.getByRole('button', { name: 'Aprobar identidad' }).click();
  const approved = admin.getByRole('table', { name: 'Cuentas OIDC aprobadas' });
  await expect(approved).toContainText('Ada Externa');
  await expect(approved).toContainText('acme-identity');

  // The same browser now signs in through the provider and reaches the app.
  await external.goto(origin + '/');
  await external.getByRole('link', { name: 'Continuar con el proveedor' }).click();
  await expect(external.getByRole('button', { name: 'Abrir menú de usuario' })).toBeVisible();
  const status = await external.evaluate(() => fetch('/api/auth/status').then((response) => response.json()));
  if (status.mode !== 'oidc' || status.authenticated !== true || status.user?.auth_source !== 'oidc' || status.user?.name !== 'Ada Externa') {
    throw new Error(`Unexpected OIDC status: ${JSON.stringify(status)}`);
  }

  console.log('OIDC mode + Go + SQLite + real web: console draft/test/activation, pending identity approval with scoped connection, provider login and OIDC session OK.');
} finally {
  if (server && server.exitCode === null && server.signalCode === null) {
    const exited = new Promise((done) => server.once('exit', done));
    server.kill('SIGTERM');
    await exited;
  }
  if (provider) await new Promise((done) => provider.close(done));
  if (browser) await browser.close();
  await rm(temporary, { recursive: true, force: true });
}
