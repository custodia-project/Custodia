import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { cp, mkdir, mkdtemp, readFile, readdir, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, relative, resolve } from 'node:path';

// Build a self-hosted package from compiled public artifacts only. Source,
// fixtures, local databases, tokens and repository configuration never enter
// the deliverable. No upload or publication is performed.
const root = resolve(import.meta.dirname, '..');
const args = process.argv.slice(2);
const outIndex = args.indexOf('--out');
const outputRoot = resolve(outIndex >= 0 ? args[outIndex + 1] : join(root, 'artifacts'));
const keyIndex = args.indexOf('--signing-key');
const signingKey = keyIndex >= 0 ? resolve(args[keyIndex + 1]) : null;
const version = process.env.CUSTODIA_VERSION || '0.0.0-dev';
const platform = process.platform;
const architecture = process.arch === 'x64' ? 'amd64' : process.arch;
const packageName = `custodia-${version}-${platform}-${architecture}`;
const stagingRoot = await mkdtemp(join(tmpdir(), 'custodia-package-'));
const packageDir = join(stagingRoot, packageName);
const hash = bytes => createHash('sha256').update(bytes).digest('hex');

async function filesIn(directory, base = directory) {
  const files = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (entry.isSymbolicLink()) throw new Error(`Package contains a symbolic link: ${entry.name}`);
    const path = join(directory, entry.name);
    if (entry.isDirectory()) files.push(...await filesIn(path, base));
    else if (entry.isFile()) files.push({ path, relative: relative(base, path).split('\\').join('/') });
    else throw new Error(`Unexpected package entry: ${entry.name}`);
  }
  return files.sort((left, right) => left.relative.localeCompare(right.relative));
}

try {
  await mkdir(packageDir, { recursive: true });
  const webDir = join(packageDir, 'web');
  execFileSync('npm', ['--prefix', 'web', 'run', 'build', '--', '--outDir', webDir], {
    cwd: root, env: { ...process.env, VITE_DEMO: '0' }, stdio: 'inherit',
  });
  const binary = join(packageDir, platform === 'win32' ? 'custodia.exe' : 'custodia');
  execFileSync('go', ['build', '-trimpath', '-ldflags', `-X main.version=${version} -X 'main.releasePublicKey=${process.env.CUSTODIA_RELEASE_PUBLIC_KEY || ''}'`, '-o', binary, './cmd/custodia'], {
    cwd: join(root, 'server'), env: { ...process.env, CGO_ENABLED: '0' }, stdio: 'inherit',
  });
  await stat(join(webDir, 'index.html'));
  for (const name of ['README.md', 'README.es.md', 'LICENSE']) {
    await cp(join(root, name), join(packageDir, name));
  }
  await mkdir(join(packageDir, 'server'), { recursive: true });
  await cp(join(root, 'server', 'README.md'), join(packageDir, 'server', 'README.md'));
  await cp(join(root, 'docs'), join(packageDir, 'docs'), { recursive: true });
  await writeFile(join(packageDir, 'VERSION'), `${version}\n${platform}/${architecture}\n`, { flag: 'wx' });

  const files = [];
  for (const file of await filesIn(packageDir)) {
    const bytes = await readFile(file.path);
    files.push({ path: file.relative, bytes: bytes.length, sha256: hash(bytes) });
  }
  const manifest = `${JSON.stringify({
    format: 1,
    product: 'Custodia',
    version,
    platform,
    architecture,
    entrypoint: platform === 'win32' ? 'custodia.exe' : 'custodia',
    web: 'web/index.html',
    files,
  }, null, 2)}\n`;
  await writeFile(join(packageDir, 'manifest.json'), manifest, { flag: 'wx' });
  if (signingKey) {
    execFileSync('ssh-keygen', ['-Y', 'sign', '-f', signingKey, '-n', 'custodia-release', join(packageDir, 'manifest.json')], { stdio: 'inherit' });
  }

  const destination = join(outputRoot, packageName);
  await mkdir(outputRoot, { recursive: true });
  let created = false;
  try {
    await mkdir(destination);
    created = true;
  } catch (error) {
    if (error.code !== 'EEXIST') throw error;
    const existing = await readFile(join(destination, 'manifest.json'), 'utf8');
    if (existing !== manifest) throw new Error('Existing package has a different manifest');
    for (const file of files) {
      const bytes = await readFile(join(destination, file.path));
      if (bytes.length !== file.bytes || hash(bytes) !== file.sha256) throw new Error(`Existing package file changed: ${file.path}`);
    }
  }
  if (created) {
    for (const file of await filesIn(packageDir)) {
      const target = join(destination, file.relative);
      await mkdir(join(target, '..'), { recursive: true });
      await cp(file.path, target);
    }
  }
  const archive = `${destination}.tar.gz`;
  execFileSync('tar', ['-czf', archive, '-C', created ? stagingRoot : outputRoot, packageName], { stdio: 'inherit' });
  process.stdout.write(`Custodia package: ${destination}\n`);
  process.stdout.write(`Archive: ${archive}\n`);
  process.stdout.write(`Manifest SHA-256: ${hash(Buffer.from(manifest))}\n`);
  process.stdout.write('No upload or publication performed.\n');
} finally {
  await rm(stagingRoot, { recursive: true, force: true });
}
