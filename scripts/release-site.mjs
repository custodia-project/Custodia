// Prepare what the product site serves for installation and updates, signed
// with the release key. It writes files only; publishing them is a separate,
// explicit step.
//
//   node scripts/release-site.mjs --signing-key KEY --version 0.1.0 \
//     [--arch amd64,arm64] [--channel stable] [--base-url https://...] [--out site/public]
//
// Layout under --out:
//   install.sh, install.sh.sig        installer with the release key embedded
//   allowed_signers                   the same key, to compare it another way
//   releases/v<V>/custodia-<V>-linux-<arch>.tar.gz (+ .manifest.json.sig)
//   releases/v<V>/linux-<arch>.json (+ .sig)        signed catalog of that version
//   releases/<channel>/linux-<arch>.json (+ .sig)   signed catalog of the channel
// The catalog uses the console update-manifest format, so installed servers
// read the same channel to offer updates.
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const args = process.argv.slice(2);
const option = (name, fallback) => {
  const index = args.indexOf(name);
  return index >= 0 ? args[index + 1] : fallback;
};
const signingKey = option('--signing-key');
const version = option('--version');
if (!signingKey || !version) throw new Error('usage: release-site.mjs --signing-key KEY --version V [--arch amd64,arm64] [--channel stable] [--base-url URL] [--out DIR]');
if (!/^\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$/.test(version)) throw new Error(`invalid version: ${version}`);
const architectures = option('--arch', 'amd64,arm64').split(',').filter(Boolean);
const channel = option('--channel', 'stable');
const baseURL = option('--base-url', 'https://custodia.octaviosantacruz.com').replace(/\/$/, '');
if (!baseURL.startsWith('https://')) throw new Error('--base-url must be HTTPS');
const out = resolve(option('--out', join(root, 'site', 'public')));

const sign = (file) => execFileSync('ssh-keygen', ['-Y', 'sign', '-f', resolve(signingKey), '-n', 'custodia-release', file], { stdio: 'pipe' });
const signFresh = async (file) => {
  await rm(`${file}.sig`, { force: true });
  sign(file);
};
const sha256 = async (file) => createHash('sha256').update(await readFile(file)).digest('hex');

const publicKey = (await readFile(`${resolve(signingKey)}.pub`, 'utf8')).trim().split(/\s+/).slice(0, 2).join(' ');
const signersLine = `custodia-release ${publicKey}`;
const versionDir = join(out, 'releases', `v${version}`);
const channelDir = join(out, 'releases', channel);
await mkdir(versionDir, { recursive: true });
await mkdir(channelDir, { recursive: true });

const staging = await mkdtemp(join(tmpdir(), 'custodia-release-'));
try {
  for (const architecture of architectures) {
    execFileSync('node', [join(root, 'scripts', 'package-go.mjs'), '--out', staging, '--signing-key', resolve(signingKey)], {
      cwd: root, env: { ...process.env, CUSTODIA_VERSION: version, CUSTODIA_ARCH: architecture }, stdio: 'inherit',
    });
    const name = `custodia-${version}-linux-${architecture}`;
    const archive = join(versionDir, `${name}.tar.gz`);
    const signature = join(versionDir, `${name}.manifest.json.sig`);
    await copyFile(join(staging, `${name}.tar.gz`), archive);
    await copyFile(join(staging, name, 'manifest.json.sig'), signature);
    const catalog = `${JSON.stringify({
      version,
      channel,
      architecture,
      package: `${baseURL}/releases/v${version}/${name}.tar.gz`,
      sha256: await sha256(archive),
      signature: `${baseURL}/releases/v${version}/${name}.manifest.json.sig`,
      published_at: new Date().toISOString(),
    }, null, 2)}\n`;
    for (const file of [join(versionDir, `linux-${architecture}.json`), join(channelDir, `linux-${architecture}.json`)]) {
      await writeFile(file, catalog);
      await signFresh(file);
    }
  }
} finally {
  await rm(staging, { recursive: true, force: true });
}

const template = await readFile(join(root, 'scripts', 'install.sh'), 'utf8');
if (!template.includes("RELEASE_KEY='__CUSTODIA_RELEASE_KEY__'")) throw new Error('install.sh has no release key placeholder');
const installer = template
  .replace("RELEASE_KEY='__CUSTODIA_RELEASE_KEY__'", `RELEASE_KEY='${signersLine}'`)
  .replace(/^BASE_URL=.*$/m, `BASE_URL=${baseURL}`);
await writeFile(join(out, 'install.sh'), installer, { mode: 0o644 });
await signFresh(join(out, 'install.sh'));
await writeFile(join(out, 'allowed_signers'), `${signersLine}\n`);
console.log(`Release site files for ${version} (${architectures.join(', ')}, channel ${channel}) written to ${out}. Nothing was published.`);
