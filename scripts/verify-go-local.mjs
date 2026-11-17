// Local login end to end in Chromium against the real Go server and SQLite.
// Creates the first administrator in the browser with the one-use setup code
// the host prints, then exercises login, the generic error, the temporary
// password, account creation and disabling (which revokes the reader's live
// session).
import { spawn, execFileSync } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer as createNetServer } from 'node:net';
import { chromium, expect } from '../web/node_modules/@playwright/test/index.mjs';

const root = resolve(import.meta.dirname, '..');
const temporary = await mkdtemp(join(tmpdir(), 'custodia-local-'));
const binary = join(temporary, 'custodia');
const data = join(temporary, 'data');
const webBuild = join(temporary, 'web');
const adminPassword = 'local browser passphrase 16';
const readerPassword = 'temporary reader passphrase';
const newReaderPassword = 'a brand new reader passphrase';
let server, browser;

async function freePort() {
  const probe = createNetServer();
  await new Promise((done) => probe.listen(0, '127.0.0.1', done));
  const port = probe.address().port;
  await new Promise((done) => probe.close(done));
  return port;
}

try {
  execFileSync('npm', ['--prefix', 'web', 'run', 'build', '--', '--outDir', webBuild], { cwd: root, env: { ...process.env, VITE_DEMO: '0' }, stdio: 'inherit' });
  execFileSync('go', ['build', '-o', binary, './cmd/custodia'], { cwd: join(root, 'server'), env: { ...process.env, CGO_ENABLED: '0' }, stdio: 'inherit' });
  execFileSync(binary, ['init', '--dir', data], { stdio: 'pipe' });
  // The host prints a one-use setup code; no password is printed or piped.
  const issued = execFileSync(binary, ['setup-code', '--dir', data], { encoding: 'utf8' });
  const setupCode = issued.match(/Setup code: (\S+)/)?.[1];
  if (!setupCode) throw new Error(`setup-code printed no code: ${issued}`);

  const port = await freePort();
  const origin = `http://127.0.0.1:${port}`;
  server = spawn(binary, ['serve', '--auth-mode', 'local', '--public-origin', origin, '--dir', data, '--web-dir', webBuild, '--listen', `127.0.0.1:${port}`], { stdio: ['ignore', 'pipe', 'pipe'] });
  let failure = '';
  server.stderr.on('data', (chunk) => { failure += chunk; });
  await new Promise((done, fail) => {
    const timer = setTimeout(() => fail(Error('Local server startup timed out')), 10_000);
    server.stdout.once('data', () => { clearTimeout(timer); done(); });
    server.once('exit', (code) => { clearTimeout(timer); fail(Error(`Local server exited ${code}: ${failure}`)); });
  });

  browser = await chromium.launch();
  const admin = await (await browser.newContext({ locale: 'es-AR' })).newPage();
  admin.on('pageerror', (error) => console.error('PAGEERROR', error.message));
  admin.on('console', (message) => { if (message.type() === 'error') console.error('CONSOLE', message.text().slice(0, 500)); });
  await admin.goto(origin + '/');
  // First run: the browser setup with the code from the host.
  await expect(admin.getByRole('heading', { name: 'Configurá Custodia' })).toBeVisible();
  await admin.getByRole('button', { name: 'Empezar' }).click();
  await admin.getByLabel('Código de configuración').fill('ZZZZ-ZZZZ');
  await admin.getByLabel('Nombre', { exact: true }).fill('Ada Admin');
  await admin.getByRole('textbox', { name: 'Usuario', exact: true }).fill('ada.admin');
  await admin.getByRole('button', { name: 'Continuar' }).click();
  await admin.locator('input[autocomplete="new-password"]').nth(0).fill(adminPassword);
  await admin.locator('input[autocomplete="new-password"]').nth(1).fill(adminPassword);
  await admin.getByRole('button', { name: 'Crear administrador' }).click();
  await expect(admin.locator('.auth-error')).toContainText('El código no es válido o venció');
  await admin.getByRole('button', { name: 'Volver' }).click();
  await admin.getByLabel('Código de configuración').fill(setupCode.toLowerCase());
  await admin.getByRole('button', { name: 'Continuar' }).click();
  await admin.getByRole('button', { name: 'Crear administrador' }).click();
  await expect(admin.getByRole('button', { name: 'Abrir menú de usuario' })).toBeVisible();
  // The setup path is closed now; a fresh visitor gets the normal login.
  const visitor = await (await browser.newContext({ locale: 'es-AR' })).newPage();
  await visitor.goto(origin + '/');
  await expect(visitor.getByRole('heading', { name: 'Iniciar sesión' })).toBeVisible();
  await visitor.getByRole('textbox', { name: 'Usuario', exact: true }).fill('ada.admin');
  await visitor.locator('input[type="password"]').fill('not the password');
  await visitor.getByRole('button', { name: 'Iniciar sesión' }).click();
  await expect(visitor.locator('.auth-error')).toHaveText('Usuario o contraseña incorrectos.');
  await visitor.locator('input[type="password"]').fill(adminPassword);
  await visitor.getByRole('button', { name: 'Iniciar sesión' }).click();
  await expect(visitor.getByRole('button', { name: 'Abrir menú de usuario' })).toBeVisible();
  await visitor.close();
  await admin.goto(origin + '/admin/access');
  await expect(admin.getByRole('heading', { name: 'Acceso a la plataforma' })).toBeVisible();
  await expect(admin.getByRole('row', { name: /ada\.admin/ })).toBeVisible();

  await admin.getByRole('button', { name: 'Crear usuario local' }).first().click();
  const drawer = admin.getByRole('dialog', { name: 'Crear usuario local' });
  await drawer.getByLabel('Usuario', { exact: true }).fill('bruno');
  await drawer.getByLabel('Nombre', { exact: true }).fill('Bruno Reader');
  await drawer.getByLabel('Contraseña temporal').fill(readerPassword);
  await drawer.getByRole('button', { name: 'Crear usuario local' }).click();
  const brunoRow = admin.getByRole('row', { name: /bruno/ });
  await expect(brunoRow).toBeVisible();
  await expect(brunoRow.getByText('Cambio pendiente')).toBeVisible();

  const reader = await (await browser.newContext({ locale: 'es-AR' })).newPage();
  await reader.goto(origin + '/');
  await reader.getByRole('textbox', { name: 'Usuario', exact: true }).fill('bruno');
  await reader.locator('input[type="password"]').fill(readerPassword);
  await reader.getByRole('button', { name: 'Iniciar sesión' }).click();
  await expect(reader.getByRole('heading', { name: 'Elegí tu contraseña' })).toBeVisible();
  await reader.getByLabel('Contraseña actual').fill(readerPassword);
  await reader.getByLabel(/^Nueva contraseña/).fill(newReaderPassword);
  await reader.getByLabel('Confirmar contraseña').fill(newReaderPassword);
  await reader.getByRole('button', { name: 'Guardar contraseña' }).click();
  await expect(reader.getByRole('button', { name: 'Abrir menú de usuario' })).toBeVisible();

  await admin.reload();
  const updatedRow = admin.getByRole('row', { name: /bruno/ });
  await expect(updatedRow.getByText('Cambio pendiente')).toHaveCount(0);
  await updatedRow.getByRole('button', { name: 'Deshabilitar' }).click();
  await expect(updatedRow.getByText('Deshabilitado')).toBeVisible();
  await reader.reload();
  await expect(reader.getByRole('heading', { name: 'Iniciar sesión' })).toBeVisible();

  console.log('Local mode + Go + SQLite + real web: first administrator by browser setup code (wrong code refused, then closed), generic login error, session cookie, temporary password change, account creation and disabling with session revocation OK.');
} finally {
  if (server && server.exitCode === null && server.signalCode === null) {
    const exited = new Promise((done) => server.once('exit', done));
    server.kill('SIGTERM');
    await exited;
  }
  if (browser) await browser.close();
  await rm(temporary, { recursive: true, force: true });
}
