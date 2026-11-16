import { execFileSync, spawn } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { access, mkdir, mkdtemp, readFile, readdir, rm, stat, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

// Clean disposable host gate for the generated package. No active service,
// repository database, token or deployment is touched.
const root = resolve(import.meta.dirname, '..');
const temporary = await mkdtemp(join(tmpdir(), 'custodia-go-host-'));
const packageOut = join(temporary, 'package');
const host = join(temporary, 'host');
const source = join(temporary, 'source');
const restored = join(temporary, 'restored');
const backup = join(temporary, 'state.backup');
let server;

const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const freePort = async () => {
  const probe = createServer();
  await new Promise(resolve => probe.listen(0, '127.0.0.1', resolve));
  const port = probe.address().port;
  await new Promise(resolve => probe.close(resolve));
  return port;
};

const start = async (directory, token, port, webDir) => {
  server = spawn(join(webDir, '..', 'custodia'), ['serve-lab', '--dir', directory, '--listen', `127.0.0.1:${port}`, '--web-dir', webDir], { stdio: ['ignore', 'pipe', 'pipe'] });
  await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error('Packaged server did not start')), 10000);
    server.once('exit', code => { clearTimeout(timeout); reject(new Error(`Packaged server exited ${code}`)); });
    server.stdout.once('data', () => { clearTimeout(timeout); resolve(); });
  });
  const page = await fetch(`http://127.0.0.1:${port}/`);
  if (page.status !== 200 || !(await page.text()).includes('id="root"')) throw new Error('Packaged web entrypoint is missing');
  const response = await fetch(`http://127.0.0.1:${port}/api/auth/status`, { headers: { Authorization: `Bearer ${token}` } });
  if (response.status !== 200) throw new Error(`Packaged auth status returned ${response.status}`);
  const status = await response.json();
  if (!status.authenticated) throw new Error('Packaged instance is not authenticated');
};

const stop = async () => {
  if (!server || server.exitCode !== null || server.signalCode !== null) return;
  const exited = new Promise(resolve => server.once('exit', resolve));
  server.kill('SIGTERM');
  await exited;
  server = undefined;
};

try {
  execFileSync(process.execPath, ['scripts/package-go.mjs', '--out', packageOut], {
    cwd: root, env: { ...process.env, CUSTODIA_VERSION: '0.0.0-dev' }, stdio: 'inherit',
  });
  const packages = (await readdir(packageOut)).filter(name => !name.endsWith('.tar.gz'));
  if (packages.length !== 1) throw new Error(`Expected one package, got ${packages.length}`);
  const packageName = packages[0];
  const archive = join(packageOut, `${packageName}.tar.gz`);
  const packageDir = join(packageOut, packageName);
  const manifest = JSON.parse(await readFile(join(packageDir, 'manifest.json'), 'utf8'));
  if (manifest.product !== 'Custodia' || manifest.version !== '0.0.0-dev') throw new Error('Package manifest changed');
  for (const file of manifest.files) {
    const bytes = await readFile(join(packageDir, file.path));
    if (bytes.length !== file.bytes || hash(bytes) !== file.sha256) throw new Error(`Package file changed: ${file.path}`);
    if (/(^|\/)(state\.db|lab\.token|\.env)(\/|$)/.test(file.path) || file.path.endsWith('.db')) {
      throw new Error(`Package contains local state: ${file.path}`);
    }
  }
  for (const required of ['custodia', 'web/index.html', 'LICENSE', 'server/README.md', 'docs/PROBE.md']) {
    if (!manifest.files.some(file => file.path === required)) throw new Error(`Package missing ${required}`);
  }

  await mkdir(host, { mode: 0o700 });
  execFileSync('tar', ['-xzf', archive, '-C', host], { stdio: 'inherit' });
  const extracted = join(host, packageName);
  const binary = join(extracted, 'custodia');
  const webDir = join(extracted, 'web');
  await stat(binary);
  await stat(join(webDir, 'index.html'));

  execFileSync(binary, ['init-lab', '--dir', source], { stdio: 'pipe' });
  const sourceToken = await readFile(join(source, 'lab.token'), 'utf8');
  const sourcePort = await freePort();
  await start(source, sourceToken, sourcePort, webDir);
  // Backup with the packaged service running.
  execFileSync(binary, ['backup', '--dir', source, '--out', backup], { stdio: 'inherit' });
  await stop();

  // Idempotent migration/restart check on the installed host.
  execFileSync(binary, ['migrate-lab', '--dir', source], { stdio: 'inherit' });
  execFileSync(binary, ['migrate-lab', '--dir', source], { stdio: 'inherit' });
  await start(source, sourceToken, await freePort(), webDir);
  await stop();

  await mkdir(restored, { mode: 0o700 });
  execFileSync(binary, ['restore', '--dir', restored, '--from', backup], { stdio: 'inherit' });
  const restoredToken = randomBytes(32).toString('hex');
  await writeFile(join(restored, 'lab.token'), restoredToken, { mode: 0o600 });
  execFileSync(binary, ['migrate-lab', '--dir', restored], { stdio: 'inherit' });
  execFileSync(binary, ['migrate-lab', '--dir', restored], { stdio: 'inherit' });
  await start(restored, restoredToken, await freePort(), webDir);
  await stop();

  await access(backup);
  console.log('Packaged clean-host gate: manifest, install, migration x2, online backup, restore, restart and packaged web/API OK.');
} finally {
  await stop();
  await rm(temporary, { recursive: true, force: true });
}
