// Production bundle gate: build the MVP profile and prove that the demo-only
// tools (Netskope SSE, SIA/Risk, Personas, DSPM and their coverage view) emit
// no chunks. The demo build is checked separately by verify:web.
import { execFileSync } from 'node:child_process';
import { readdirSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const web = join(root, 'web');
execFileSync('npm', ['run', 'build'], { cwd: web, stdio: 'inherit' });
const assets = join(web, 'dist', 'assets');
const names = readdirSync(assets);
const forbidden = /(netskope|sia[-_]|person|personas|dspm|risk|identity-workspace|connection-inventory)/i;
const leaked = names.filter((name) => forbidden.test(name));
if (leaked.length) throw new Error(`production bundle leaked legacy chunks: ${leaked.join(', ')}`);
// Vendor marks of tools outside the MVP must not ship either.
const marks = readdirSync(join(web, 'dist')).filter((name) => /^(netskope|bigid)[-_.]/i.test(name));
if (marks.length) throw new Error(`production build ships non-MVP vendor marks: ${marks.join(', ')}`);
let bytes = 0;
for (const name of names) bytes += statSync(join(assets, name)).size;
if (!names.some((name) => name.startsWith('index-'))) throw new Error('production bundle has no entry chunk');
console.log(`Production core bundle: ${names.length} assets, ${(bytes / 1024 / 1024).toFixed(2)} MiB, no Netskope/SIA/Risk/Personas/DSPM chunks or vendor marks.`);
