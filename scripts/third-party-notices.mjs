// Third-party notices for what a Custodia package distributes: the Go
// modules linked into the binary (plus the Go standard library) and the npm
// production dependency tree of the web build. It copies each license, NOTICE
// and PATENTS file verbatim and fails when a license is not in the permissive
// allowlist or a component has no license text, so a new dependency cannot
// enter a release silently.
//
// Usage: node scripts/third-party-notices.mjs [--out FILE] [--check]
import { execFileSync } from 'node:child_process';
import { existsSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const args = process.argv.slice(2);
const outIndex = args.indexOf('--out');
const out = outIndex >= 0 ? resolve(args[outIndex + 1]) : null;
const checkOnly = args.includes('--check');

// SPDX identifiers accepted without review. Anything else stops the build.
const ALLOWED = new Set(['MIT', 'Apache-2.0', 'BSD-2-Clause', 'BSD-3-Clause', 'ISC', '0BSD', 'OFL-1.1', 'Unlicense', 'blessing']);
const NOTICE_FILE = /^(licen[cs]e|copying|notice|patents)([-._].*)?$/i;

// Components whose package carries no license file. Each override names the
// license text that applies and why; it is reviewed, not guessed.
const OVERRIDES = {
  'client-only': { license: 'MIT', textFrom: 'react', note: 'Marker package published by the React project without its own license file; React\'s MIT license applies.' },
};

// Go modules whose license is identified here rather than parsed from text.
const GO_LICENSES = {
  'github.com/coreos/go-oidc/v3': 'Apache-2.0',
  'github.com/go-jose/go-jose/v4': 'Apache-2.0',
  'github.com/dustin/go-humanize': 'MIT',
  'github.com/google/uuid': 'BSD-3-Clause',
  'github.com/remyoudompheng/bigfft': 'BSD-3-Clause',
  'golang.org/x/crypto': 'BSD-3-Clause',
  'golang.org/x/oauth2': 'BSD-3-Clause',
  'golang.org/x/sys': 'BSD-3-Clause',
  'modernc.org/libc': 'BSD-3-Clause',
  'modernc.org/mathutil': 'BSD-3-Clause',
  'modernc.org/memory': 'BSD-3-Clause',
  'modernc.org/sqlite': 'BSD-3-Clause',
};

const failures = [];
const noticeFiles = (dir) => (existsSync(dir) ? readdirSync(dir).filter((name) => NOTICE_FILE.test(name)).sort() : []);
const texts = (dir) => noticeFiles(dir).map((name) => ({ name, text: readFileSync(join(dir, name), 'utf8').trim() }));

function goComponents() {
  const server = join(root, 'server');
  const listing = execFileSync('go', ['list', '-deps', '-f', '{{if .Module}}{{.Module.Path}}\t{{.Module.Version}}\t{{.Module.Dir}}{{end}}', './cmd/custodia'], { cwd: server, encoding: 'utf8' });
  const modules = new Map();
  for (const line of listing.split('\n')) {
    const [path, version, dir] = line.split('\t');
    if (!path || path === 'custodia' || path.startsWith('custodia/') || modules.has(path)) continue;
    modules.set(path, { name: path, version, dir });
  }
  const components = [];
  for (const module of [...modules.values()].sort((a, b) => a.name.localeCompare(b.name))) {
    const license = GO_LICENSES[module.name];
    if (!license) failures.push(`Go module ${module.name} has no reviewed license entry in GO_LICENSES`);
    const files = texts(module.dir);
    if (!files.length) failures.push(`Go module ${module.name} has no license file`);
    components.push({ ecosystem: 'Go', name: module.name, version: module.version, license: license ?? 'UNKNOWN', files });
  }
  const goroot = execFileSync('go', ['env', 'GOROOT'], { encoding: 'utf8' }).trim();
  const goVersion = execFileSync('go', ['env', 'GOVERSION'], { encoding: 'utf8' }).trim();
  components.unshift({ ecosystem: 'Go', name: 'Go standard library and runtime', version: goVersion, license: 'BSD-3-Clause', files: texts(goroot).filter((file) => /^(license|patents)$/i.test(file.name)) });
  return components;
}

function webComponents() {
  const web = join(root, 'web');
  const seen = new Map();
  const locate = (name, from) => {
    for (let dir = from; ; dir = dirname(dir)) {
      const candidate = join(dir, 'node_modules', name, 'package.json');
      if (existsSync(candidate)) return candidate;
      if (dirname(dir) === dir) return null;
    }
  };
  const walk = (name, from) => {
    const manifest = locate(name, from);
    if (!manifest) { failures.push(`npm package ${name} is not installed`); return; }
    const pkg = JSON.parse(readFileSync(manifest, 'utf8'));
    const key = `${pkg.name}@${pkg.version}`;
    if (seen.has(key)) return;
    const dir = dirname(manifest);
    const declared = typeof pkg.license === 'string' ? pkg.license : null;
    const component = { ecosystem: 'npm', name: pkg.name, version: pkg.version, license: declared ?? 'UNKNOWN', files: texts(dir) };
    const override = OVERRIDES[pkg.name];
    if (override) {
      const source = locate(override.textFrom, dir);
      component.license = override.license;
      component.note = override.note;
      component.files = source ? texts(dirname(source)) : [];
    }
    if (!component.files.length) failures.push(`npm package ${key} has no license text`);
    seen.set(key, component);
    for (const dependency of Object.keys(pkg.dependencies ?? {})) walk(dependency, dir);
    for (const dependency of Object.keys(pkg.optionalDependencies ?? {})) if (locate(dependency, dir)) walk(dependency, dir);
  };
  const app = JSON.parse(readFileSync(join(web, 'package.json'), 'utf8'));
  for (const dependency of Object.keys(app.dependencies ?? {})) walk(dependency, web);
  return [...seen.values()].sort((a, b) => a.name.localeCompare(b.name));
}

const components = [...goComponents(), ...webComponents()];
for (const component of components) {
  const ids = component.license.replace(/[()]/g, ' ').split(/\s+(?:OR|AND)\s+|\s+/).filter(Boolean);
  if (!ids.length || ids.some((id) => !ALLOWED.has(id))) failures.push(`${component.name}@${component.version} has license "${component.license}" outside the allowlist`);
}

if (failures.length) {
  console.error(`Third-party notices: ${failures.length} problem(s)`);
  for (const failure of failures) console.error(`- ${failure}`);
  process.exit(1);
}

const lines = [
  '# Third-party notices',
  '',
  'Custodia is licensed under the Apache License 2.0 (see LICENSE). This',
  'package also contains the third-party components listed below, each under',
  'its own license. The full license, NOTICE and PATENTS texts follow the',
  'table, copied verbatim from each component. Generated by',
  '`scripts/third-party-notices.mjs`; do not edit by hand.',
  '',
  '## Trademarks',
  '',
  'CyberArk, Wazuh and the other product names shown in Custodia are',
  'trademarks of their respective owners. They are used only to identify the',
  'tools Custodia can read from. Custodia is not affiliated with, endorsed by',
  'or sponsored by any of them.',
  '',
  '## Components',
  '',
  '| Ecosystem | Component | Version | License |',
  '|---|---|---|---|',
  ...components.map((c) => `| ${c.ecosystem} | ${c.name} | ${c.version} | ${c.license} |`),
  '',
  '## License texts',
  '',
];
for (const component of components) {
  lines.push(`### ${component.name} ${component.version} (${component.license})`, '');
  if (component.note) lines.push(component.note, '');
  for (const file of component.files) lines.push(`#### ${file.name}`, '', '```text', file.text, '```', '');
}
const document = `${lines.join('\n').trimEnd()}\n`;

const counts = components.reduce((acc, c) => ({ ...acc, [c.license]: (acc[c.license] ?? 0) + 1 }), {});
const summary = Object.entries(counts).map(([license, count]) => `${license} ${count}`).join(', ');
if (out && !checkOnly) writeFileSync(out, document);
console.log(`Third-party notices: ${components.length} components (${summary}); all licenses allowed${out && !checkOnly ? `; written to ${out}` : ''}.`);
