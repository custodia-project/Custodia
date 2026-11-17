// Installer gate: builds a signed local release, installs it into a temporary
// prefix without systemd, verifies TLS/health/one-time admin, rejects a bad
// signature and checks uninstall. It never touches host services or /var.
import { execFileSync, spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const temporary = await mkdtemp(join(tmpdir(), 'custodia-install-'));
const version = '0.1.0-test';
const prefix = join(temporary, 'prefix');
const data = join(temporary, 'data');
const config = join(temporary, 'config');
const key = join(temporary, 'release-key');
const allowed = join(temporary, 'allowed-signers');
const out = join(temporary, 'artifacts');

function freePort() {
  return new Promise((done) => {
    const server = createServer();
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      server.close(() => done(port));
    });
  });
}
function run(command, args) {
  return execFileSync(command, args, { encoding: 'utf8', stdio: ['pipe', 'pipe', 'pipe'] });
}
try {
  execFileSync('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-f', key], { stdio: 'pipe' });
  const publicKey = (await readFile(`${key}.pub`, 'utf8')).trim().split(/\s+/).slice(0, 2).join(' ');
  await writeFile(allowed, `custodia-release ${publicKey}\n`, { mode: 0o600 });
  execFileSync('node', ['scripts/package-go.mjs', '--out', out, '--signing-key', key], {
    cwd: root, env: { ...process.env, CUSTODIA_VERSION: version }, stdio: 'pipe',
  });
  const archive = join(out, `custodia-${version}-linux-amd64.tar.gz`);
  const packageName = `custodia-${version}-linux-amd64`;
  let archivePath = archive;
  try { await readFile(archivePath); } catch { archivePath = join(out, `${packageName}.tar.gz`); }
  const bytes = await readFile(archivePath);
  const sha = createHash('sha256').update(bytes).digest('hex');
  const packageDir = join(out, packageName);
  const signature = join(packageDir, 'manifest.json.sig');
  const port = await freePort();
  const origin = `https://127.0.0.1:${port}`;
  const installArgs = [
    '--no-systemd', '--version', version, '--package', archivePath, '--sha256', sha,
    '--signature', signature, '--allowed-signers', allowed,
    '--prefix', prefix, '--data-dir', data, '--config-dir', config,
    '--public-origin', origin, '--listen', `127.0.0.1:${port}`,
  ];
  const installed = run('sh', [join(root, 'scripts/install.sh'), ...installArgs]);
  const password = installed.match(/one-time password: (\S+)/)?.[1];
  if (!password) throw new Error(`installer did not print a one-time password: ${installed}`);
  if (!installed.includes(`Custodia ${version} installed.`)) throw new Error('installer did not report success');
  const versionOutput = run(join(prefix, 'bin', 'custodia'), ['version']).trim();
  if (versionOutput !== version) throw new Error(`version = ${versionOutput}`);
  for (const path of [join(data, 'state.db'), join(config, 'master.key'), join(config, 'tls', 'server.crt'), join(config, 'tls', 'server.key')]) {
    await readFile(path);
  }

  // Each rejection must fail before any host mutation and for its own reason.
  const rejectInstall = (args, reason) => {
    const badPrefix = join(temporary, `bad-prefix-${reason.length}`);
    let message = '';
    try {
      run('sh', [join(root, 'scripts/install.sh'), ...args, '--prefix', badPrefix]);
    } catch (error) { message = String(error.stderr ?? error.message); }
    if (!message) throw new Error(`installer accepted: ${reason}`);
    if (!message.includes(reason)) throw new Error(`installer failed for another reason (wanted "${reason}"): ${message}`);
  };
  const withoutPrefix = installArgs.filter((value, index) => installArgs[index - 1] !== '--prefix' && value !== '--prefix');
  // A release key other than the one that signed the manifest.
  const otherKey = join(temporary, 'other-key');
  execFileSync('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-f', otherKey], { stdio: 'pipe' });
  const otherPublic = (await readFile(`${otherKey}.pub`, 'utf8')).trim().split(/\s+/).slice(0, 2).join(' ');
  const otherSigners = join(temporary, 'other-signers');
  await writeFile(otherSigners, `custodia-release ${otherPublic}\n`, { mode: 0o600 });
  rejectInstall(withoutPrefix.map((value) => (value === allowed ? otherSigners : value)), 'release signature is invalid');
  // A repacked archive that keeps the genuine signed manifest but swaps the
  // binary: the signature still verifies, the per-file digests must not.
  const repack = join(temporary, 'repack');
  run('mkdir', ['-p', repack]);
  run('tar', ['-xzf', archivePath, '-C', repack]);
  await writeFile(join(repack, packageName, 'custodia'), '#!/bin/sh\necho tampered\n', { mode: 0o755 });
  const tampered = join(temporary, 'tampered.tar.gz');
  run('tar', ['-czf', tampered, '-C', repack, packageName]);
  const tamperedSha = createHash('sha256').update(await readFile(tampered)).digest('hex');
  rejectInstall(withoutPrefix.map((value) => (value === archivePath ? tampered : value === sha ? tamperedSha : value)), 'package files do not match the signed manifest');
  // An extra file next to the genuine ones is refused too.
  await writeFile(join(repack, packageName, 'custodia'), await readFile(join(packageDir, 'custodia')), { mode: 0o755 });
  await writeFile(join(repack, packageName, 'web', 'injected.js'), 'alert(1)\n');
  const extra = join(temporary, 'extra.tar.gz');
  run('tar', ['-czf', extra, '-C', repack, packageName]);
  const extraSha = createHash('sha256').update(await readFile(extra)).digest('hex');
  rejectInstall(withoutPrefix.map((value) => (value === archivePath ? extra : value === sha ? extraSha : value)), 'not listed in the signed manifest');

  // Start the installed binary and prove health plus the one-time admin flow.
  const server = spawn(join(prefix, 'bin', 'custodia'), ['serve', '--auth-mode', 'local', '--public-origin', origin, '--dir', data, '--web-dir', join(prefix, 'lib', 'custodia', 'web'), '--listen', `127.0.0.1:${port}`, '--tls-cert', join(config, 'tls', 'server.crt'), '--tls-key', join(config, 'tls', 'server.key'), '--master-key', join(config, 'master.key')], { stdio: ['ignore', 'pipe', 'pipe'], detached: true });
  server.unref();
  let healthCode = '000';
  for (let attempt = 0; attempt < 40; attempt += 1) {
    healthCode = run('curl', ['-ks', '-o', '/dev/null', '-w', '%{http_code}', `${origin}/api/health`]).trim();
    if (healthCode === '200') break;
    await new Promise((done) => setTimeout(done, 250));
  }
  if (healthCode !== '200') throw new Error(`health returned ${healthCode}`);
  const login = run('curl', ['-ks', '-H', `Origin: ${origin}`, '-H', 'Content-Type: application/json', '-d', JSON.stringify({ username: 'admin', password }), `${origin}/api/auth/login`]);
  if (!login.includes('"must_change_password":true')) throw new Error(`one-time password did not require a change: ${login}`);
  try { process.kill(-server.pid, 'SIGTERM'); } catch {}
  const uninstalled = run('sh', [join(root, 'scripts/install.sh'), '--no-systemd', '--prefix', prefix, '--data-dir', data, '--config-dir', config, '--uninstall']);
  if (!uninstalled.includes('preserved')) throw new Error('uninstall did not preserve data');
  console.log(`Installer: signed local release, local install, TLS health, one-time admin, wrong-key, tampered-binary and unlisted-file rejection and uninstall OK (version ${version}).`);
} finally {
  await rm(temporary, { recursive: true, force: true });
}
