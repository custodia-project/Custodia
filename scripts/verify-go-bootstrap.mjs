// Bootstrap gate: prepares the release site with a throwaway key, serves it
// over local HTTPS and runs the real one-liner
//   curl -fsSL BASE/install.sh | sh
// in a temporary prefix (no systemd). It checks the catalog-driven install,
// the console update channel and that a tampered catalog, a script without a
// key, a catalog pointing elsewhere and an unknown version are all refused.
// Nothing is published and no host path is touched.
import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { createServer } from 'node:https';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { extname, join, normalize, resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const temporary = await mkdtemp(join(tmpdir(), 'custodia-bootstrap-'));
const site = join(temporary, 'site');
const key = join(temporary, 'release-key');
const cert = join(temporary, 'tls.crt');
const tlsKey = join(temporary, 'tls.key');
const version = '0.1.0';
const arch = process.arch === 'x64' ? 'amd64' : process.arch;
let server;

const freePort = () => new Promise((done) => {
  const probe = createTCPServer();
  probe.listen(0, '127.0.0.1', () => { const { port } = probe.address(); probe.close(() => done(port)); });
});

try {
  execFileSync('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-f', key, '-C', 'bootstrap-gate'], { stdio: 'pipe' });
  execFileSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=127.0.0.1', '-addext', 'subjectAltName=IP:127.0.0.1', '-keyout', tlsKey, '-out', cert], { stdio: 'pipe' });
  const port = await freePort();
  const base = `https://127.0.0.1:${port}`;
  execFileSync('node', [join(root, 'scripts', 'release-site.mjs'), '--signing-key', key, '--version', version, '--arch', arch, '--base-url', base, '--out', site], { cwd: root, stdio: 'pipe' });

  const types = { '.sh': 'text/x-shellscript', '.json': 'application/json', '.gz': 'application/gzip' };
  server = createServer({ key: await readFile(tlsKey), cert: await readFile(cert) }, async (request, response) => {
    const path = normalize(decodeURIComponent(new URL(request.url, base).pathname)).replace(/^(\.\.[/\\])+/, '');
    try {
      const body = await readFile(join(site, path));
      response.writeHead(200, { 'Content-Type': types[extname(path)] ?? 'application/octet-stream' });
      response.end(body);
    } catch {
      response.writeHead(404);
      response.end();
    }
  });
  await new Promise((done) => server.listen(port, '127.0.0.1', done));

  // Inner downloads trust the local CA through CURL_CA_BUNDLE; the script
  // itself still requires HTTPS.
  const env = { ...process.env, CURL_CA_BUNDLE: cert };
  // Async: the HTTPS server runs in this process and must keep serving.
  const oneLiner = (extra, script = `${base}/install.sh`) => new Promise((done) => {
    const child = spawn('sh', ['-c', `curl -fsSL "${script}" | sh -s -- ${extra}`], { env });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (chunk) => { stdout += chunk; });
    child.stderr.on('data', (chunk) => { stderr += chunk; });
    child.on('close', (status) => done({ status, stdout, stderr }));
  });
  const prefixArgs = (name) => `--no-systemd --prefix ${join(temporary, name, 'p')} --data-dir ${join(temporary, name, 'd')} --config-dir ${join(temporary, name, 'c')} --listen 127.0.0.1:18443`;

  const installed = await oneLiner(prefixArgs('ok'));
  if (installed.status !== 0 || !installed.stdout) throw new Error(`one-liner failed (status ${installed.status}): ${installed.stderr}`);
  for (const expected of [`Custodia ${version} for linux/${arch} (signed catalog verified).`, `Custodia ${version} installed.`, 'Console address: https://', `--update-manifest ${base}/releases/stable/linux-${arch}.json`, 'one-time password:']) {
    if (!installed.stdout.includes(expected)) throw new Error(`one-liner output lacks "${expected}": ${installed.stdout}`);
  }
  const binaryVersion = execFileSync(join(temporary, 'ok', 'p', 'bin', 'custodia'), ['version'], { encoding: 'utf8' }).trim();
  if (binaryVersion !== version) throw new Error(`installed version ${binaryVersion}`);
  const pinned = (await readFile(join(temporary, 'ok', 'p', 'lib', 'custodia', 'allowed_signers'), 'utf8')).trim();
  const published = (await readFile(join(site, 'allowed_signers'), 'utf8')).trim();
  if (pinned !== published) throw new Error('the pinned release key differs from the published one');
  const scriptSignature = spawnSync('ssh-keygen', ['-Y', 'verify', '-f', join(site, 'allowed_signers'), '-I', 'custodia-release', '-n', 'custodia-release', '-s', join(site, 'install.sh.sig')], { input: await readFile(join(site, 'install.sh')) });
  if (scriptSignature.status !== 0) throw new Error('published install.sh signature does not verify');

  const refuse = (result, reason, label) => {
    if (result.status === 0) throw new Error(`${label} was accepted`);
    if (!result.stderr.includes(reason)) throw new Error(`${label} failed for another reason (wanted "${reason}"): ${result.stderr}`);
  };
  // A catalog edited after signing.
  const catalog = join(site, 'releases', 'stable', `linux-${arch}.json`);
  const original = await readFile(catalog, 'utf8');
  await writeFile(catalog, original.replace(/"sha256": "[0-9a-f]+"/, `"sha256": "${'0'.repeat(64)}"`));
  refuse(await oneLiner(prefixArgs('tampered')), 'the release catalog signature is invalid', 'a tampered catalog');
  // A correctly signed catalog that points outside the site.
  await writeFile(catalog, original.replace(`${base}/releases`, 'https://downloads.globex.example/releases'));
  await rm(`${catalog}.sig`);
  execFileSync('ssh-keygen', ['-Y', 'sign', '-f', key, '-n', 'custodia-release', catalog], { stdio: 'pipe' });
  refuse(await oneLiner(prefixArgs('elsewhere')), 'the catalog points outside', 'a catalog pointing elsewhere');
  // The repository copy has no key and must not install anything.
  await writeFile(join(site, 'unsigned-install.sh'), await readFile(join(root, 'scripts', 'install.sh')));
  refuse(await oneLiner(prefixArgs('nokey'), `${base}/unsigned-install.sh`), 'has no release key', 'a script without a release key');
  // An unknown version has no catalog.
  refuse(await oneLiner(`${prefixArgs('missing')} --version 9.9.9`), 'cannot read the release catalog', 'an unknown version');

  console.log(`Bootstrap: curl | sh from a signed catalog installed ${version} (linux/${arch}), pinned the published key, set the console update channel; tampered, redirected, keyless and unknown-version installs refused.`);
} finally {
  server?.close();
  await rm(temporary, { recursive: true, force: true });
}
