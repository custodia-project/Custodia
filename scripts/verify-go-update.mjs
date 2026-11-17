// Update gate: installs a signed N-1 release, writes real data (admin,
// encrypted connection secret), applies N with a failed health check and
// verifies rollback, then applies N successfully and verifies data/version.
// It never calls systemd and never touches host paths.
import { execFileSync, spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const temporary = await mkdtemp(join(tmpdir(), 'custodia-update-'));
const key = join(temporary, 'release-key');
const allowed = join(temporary, 'allowed-signers');
const out = join(temporary, 'artifacts');
const prefix = join(temporary, 'prefix');
const data = join(temporary, 'data');
const config = join(temporary, 'config');
const oldVersion = '0.1.0';
const newVersion = '0.2.0';
const admin = 'Update acceptance passphrase 16';
const manifestPath = join(temporary, 'release.json');
let server;

function freePort() {
  return new Promise((done) => {
    const probe = createServer();
    probe.listen(0, '127.0.0.1', () => { const { port } = probe.address(); probe.close(() => done(port)); });
  });
}
function run(command, args, options = {}) {
  return execFileSync(command, args, { encoding: 'utf8', stdio: ['pipe', 'pipe', 'pipe'], ...options });
}
async function packageVersion(version, signingKey = key) {
  run('node', ['scripts/package-go.mjs', '--out', out, '--signing-key', signingKey], { cwd: root, env: { ...process.env, CUSTODIA_VERSION: version } });
  const name = `custodia-${version}-linux-amd64`;
  const archive = join(out, `${name}.tar.gz`);
  return {
    archive,
    signature: join(out, name, 'manifest.json.sig'),
    sha: createHash('sha256').update(await readFile(archive)).digest('hex'),
  };
}
async function start(pageOrigin) {
  const port = Number(new URL(pageOrigin).port);
  server = spawn(join(prefix, 'bin', 'custodia'), ['serve', '--auth-mode', 'local', '--public-origin', pageOrigin, '--dir', data, '--web-dir', join(prefix, 'lib', 'custodia', 'web'), '--listen', `127.0.0.1:${port}`, '--tls-cert', join(config, 'tls', 'server.crt'), '--tls-key', join(config, 'tls', 'server.key'), '--master-key', join(config, 'master.key'), '--update-manifest', manifestPath], { stdio: ['ignore', 'pipe', 'pipe'], detached: true });
  server.unref();
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const code = run('curl', ['-ks', '-o', '/dev/null', '-w', '%{http_code}', `${pageOrigin}/api/health`]).trim();
    if (code === '200') return;
    await new Promise((done) => setTimeout(done, 250));
  }
  throw new Error('server did not start');
}
function stop() {
  if (!server) return;
  try { process.kill(-server.pid, 'SIGTERM'); } catch {}
  server = null;
}
function curl(args) { return run('curl', ['-ks', ...args]); }

try {
  execFileSync('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-f', key], { stdio: 'pipe' });
  const publicKey = (await readFile(`${key}.pub`, 'utf8')).trim().split(/\s+/).slice(0, 2).join(' ');
  await writeFile(allowed, `custodia-release ${publicKey}\n`, { mode: 0o600 });
  const oldPackage = await packageVersion(oldVersion);
  const newPackage = await packageVersion(newVersion);
  await writeFile(manifestPath, JSON.stringify({ version: newVersion, package: newPackage.archive, sha256: newPackage.sha, signature: newPackage.signature, allowed_signers: allowed }));
  const port = await freePort();
  const origin = `https://127.0.0.1:${port}`;
  const installed = run('sh', [join(root, 'scripts/install.sh'), '--no-systemd', '--version', oldVersion, '--package', oldPackage.archive, '--sha256', oldPackage.sha, '--signature', oldPackage.signature, '--allowed-signers', allowed, '--prefix', prefix, '--data-dir', data, '--config-dir', config, '--public-origin', origin, '--listen', `127.0.0.1:${port}`]);
  const temporaryPassword = installed.match(/one-time password: (\S+)/)?.[1];
  if (!temporaryPassword) throw new Error('installer did not print a temporary password');
  await start(origin);
  const jar = join(temporary, 'cookies');
  const login = curl(['-c', jar, '-H', `Origin: ${origin}`, '-H', 'Content-Type: application/json', '-d', JSON.stringify({ username: 'admin', password: temporaryPassword }), `${origin}/api/auth/login`]);
  if (!login.includes('"must_change_password":true')) throw new Error('temporary password did not require a change');
  let status = JSON.parse(curl(['-b', jar, '-c', jar, `${origin}/api/auth/status`]));
  curl(['-b', jar, '-c', jar, '-H', `Origin: ${origin}`, '-H', `X-Custodia-CSRF: ${status.csrf_token}`, '-H', 'Content-Type: application/json', '-d', JSON.stringify({ current_password: temporaryPassword, new_password: admin }), `${origin}/api/auth/password`]);
  status = JSON.parse(curl(['-b', jar, '-c', jar, `${origin}/api/auth/status`]));
  const connection = curl(['-b', jar, '-c', jar, '-H', `Origin: ${origin}`, '-H', `X-Custodia-CSRF: ${status.csrf_token}`, '-H', 'Content-Type: application/json', '-d', JSON.stringify({ connector: 'cyberark-pam-safes-v1', base_url: 'https://pam.acme.example', collections: ['safes'], scope: 'update-data', auth_mode: 'bearer', secret: 'update-gate-secret' }), `${origin}/api/ingestion/connections`]);
  const group = JSON.parse(connection).group_id;
  if (!group) throw new Error(`connection was not created: ${connection}`);
  const backup = curl(['-b', jar, '-c', jar, '-H', `Origin: ${origin}`, '-H', `X-Custodia-CSRF: ${status.csrf_token}`, '-H', 'Content-Type: application/json', '-d', '{}', `${origin}/api/system/backups`]);
  if (!backup.includes('"name"')) throw new Error(`console backup failed: ${backup}`);
  const backups = JSON.parse(curl(['-b', jar, `${origin}/api/system/backups`]));
  if (!backups.rows.length) throw new Error('backup list is empty');
  const backupPath = join(temporary, 'download.db');
  run('curl', ['-ks', '-b', jar, '-o', backupPath, `${origin}/api/system/backups/${encodeURIComponent(backups.rows[0].name)}`]);
  if ((await readFile(backupPath)).length < 1024) throw new Error('downloaded backup is empty');
  stop();

  // A failed health check must restore N-1, its binary and its database.
  let failed = false;
  try {
    run(join(prefix, 'bin', 'custodia'), ['update', '--dir', data, '--binary', join(prefix, 'bin', 'custodia'), '--web-dir', join(prefix, 'lib', 'custodia', 'web'), '--package', newPackage.archive, '--sha256', newPackage.sha, '--signature', newPackage.signature, '--allowed-signers', allowed, '--update-version', newVersion, '--no-systemd', '--health-url', 'http://127.0.0.1:1/health']);
  } catch { failed = true; }
  if (!failed) throw new Error('failed health check was accepted');
  const rolledBack = run(join(prefix, 'bin', 'custodia'), ['version']).trim();
  if (rolledBack !== oldVersion) throw new Error(`rollback left ${rolledBack}`);
  await start(origin);
  const relogin = curl(['-c', jar, '-H', `Origin: ${origin}`, '-H', 'Content-Type: application/json', '-d', JSON.stringify({ username: 'admin', password: admin }), `${origin}/api/auth/login`]);
  if (!relogin.includes('"authenticated":true')) throw new Error('login failed after rollback');

  // The console request is written by the service user; the root updater
  // reads it and applies the same signed release. The test runs that step.
  status = JSON.parse(curl(['-b', jar, '-c', jar, `${origin}/api/auth/status`]));
  const requested = curl(['-b', jar, '-c', jar, '-H', `Origin: ${origin}`, '-H', `X-Custodia-CSRF: ${status.csrf_token}`, '-H', 'Content-Type: application/json', '-d', '{}', `${origin}/api/system/update`]);
  if (!requested.includes('"requested":true')) throw new Error(`console update request failed: ${requested}`);
  const requestPath = join(data, '.update', 'request.json');
  await readFile(requestPath);
  stop();
  const pinned = join(prefix, 'lib', 'custodia', 'allowed_signers');
  const updateArgs = ['--dir', data, '--binary', join(prefix, 'bin', 'custodia'), '--web-dir', join(prefix, 'lib', 'custodia', 'web'), '--no-systemd', '--allowed-signers', pinned];

  // The request is written by the service user and is untrusted. A release
  // signed with another key, carrying its own allowed_signers, must be
  // rejected: the root updater only trusts the key pinned at install time.
  const legitimateRequest = await readFile(requestPath, 'utf8');
  const attackerKey = join(temporary, 'attacker-key');
  execFileSync('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-f', attackerKey], { stdio: 'pipe' });
  const attackerSigners = join(temporary, 'attacker-signers');
  const attackerPublic = (await readFile(`${attackerKey}.pub`, 'utf8')).trim().split(/\s+/).slice(0, 2).join(' ');
  await writeFile(attackerSigners, `custodia-release ${attackerPublic}\n`, { mode: 0o600 });
  const forgedVersion = '0.9.0';
  const forged = await packageVersion(forgedVersion, attackerKey);
  await writeFile(requestPath, JSON.stringify({ manifest: { version: forgedVersion, package: forged.archive, sha256: forged.sha, signature: forged.signature, allowed_signers: attackerSigners } }), { mode: 0o600 });
  let forgedError = '';
  try { run(join(prefix, 'bin', 'custodia'), ['update', '--request', requestPath, ...updateArgs]); } catch (error) { forgedError = String(error.stderr ?? error.message); }
  if (!forgedError) throw new Error('a release signed with a key from the request was applied');
  if (!forgedError.includes('release signature rejected')) throw new Error(`forged release failed for another reason: ${forgedError}`);
  if (run(join(prefix, 'bin', 'custodia'), ['version']).trim() !== oldVersion) throw new Error('forged release changed the installed version');
  // Without a pinned key the console request is refused outright.
  let unpinnedError = '';
  try { run(join(prefix, 'bin', 'custodia'), ['update', '--request', requestPath, '--dir', data, '--binary', join(prefix, 'bin', 'custodia'), '--web-dir', join(prefix, 'lib', 'custodia', 'web'), '--no-systemd']); } catch (error) { unpinnedError = String(error.stderr ?? error.message); }
  if (!unpinnedError.includes('pinned --allowed-signers')) throw new Error(`console update without a pinned key was not refused: ${unpinnedError}`);

  await writeFile(requestPath, legitimateRequest, { mode: 0o600 });
  run(join(prefix, 'bin', 'custodia'), ['update', '--request', requestPath, ...updateArgs]);
  if (run(join(prefix, 'bin', 'custodia'), ['version']).trim() !== newVersion) throw new Error('version did not advance');
  await start(origin);
  const after = JSON.parse(curl(['-c', jar, '-H', `Origin: ${origin}`, '-H', 'Content-Type: application/json', '-d', JSON.stringify({ username: 'admin', password: admin }), `${origin}/api/auth/login`]));
  if (after.authenticated !== true) throw new Error('admin lost after update');
  const connections = JSON.parse(curl(['-b', jar, `${origin}/api/ingestion/connections`]));
  const row = connections.rows.find((item) => item.group_id === group);
  if (!row || row.secret_configured !== true) throw new Error('secret connection lost after update');
  stop();
  console.log(`Update: signed ${oldVersion} -> ${newVersion}, forged-key request rejected, failed-health rollback and data/secret preservation OK.`);
} finally {
  stop();
  await rm(temporary, { recursive: true, force: true });
}
