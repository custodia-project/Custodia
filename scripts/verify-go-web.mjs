// Real frontend + Go + disposable SQLite. No API interception or mock responses.
import { spawn, execFileSync } from 'node:child_process';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer } from 'node:net';
import { chromium, expect } from '../web/node_modules/@playwright/test/index.mjs';

const root = resolve(import.meta.dirname, '..');
const temporary = await mkdtemp(join(tmpdir(), 'custodia-go-web-'));
const binary = join(temporary, 'custodia');
const webBuild = join(temporary, 'web');
let server, browser;
try {
  execFileSync('npm', ['--prefix', 'web', 'run', 'build', '--', '--outDir', webBuild], { cwd: root, env: { ...process.env, VITE_DEMO: '0' }, stdio: 'inherit' });
  execFileSync('go', ['build', '-o', binary, './cmd/custodia'], { cwd: join(root, 'server'), env: { ...process.env, CGO_ENABLED: '0' }, stdio: 'inherit' });
  execFileSync(binary, ['init-lab', '--dir', join(temporary, 'lab')], { stdio: 'pipe' });
  const token = await readFile(join(temporary, 'lab/lab.token'), 'utf8');
  const limitedFile=join(temporary,'identity.token');
  execFileSync(binary,['issue-lab','--dir',join(temporary,'lab'),'--subject','identity-reader','--connections','acme-identity','--out',limitedFile],{stdio:'pipe'});
  const limitedToken=await readFile(limitedFile,'utf8');
  const probe = createServer();
  await new Promise(resolve => probe.listen(0, '127.0.0.1', resolve));
  const port = probe.address().port;
  await new Promise(resolve => probe.close(resolve));
  const base = `http://127.0.0.1:${port}`;
  const start = async () => {
    server = spawn(binary, ['serve-lab', '--dir', join(temporary, 'lab'), '--listen', `127.0.0.1:${port}`, '--web-dir', webBuild], { stdio: ['ignore', 'pipe', 'pipe'] });
    await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error('Go server did not start')), 10000);
      server.once('exit', code => { clearTimeout(timeout); reject(new Error(`Go server exited ${code}`)); });
      server.stdout.once('data', () => { clearTimeout(timeout); resolve(); });
    });
  };
  const stop = async () => {
    if (!server || server.exitCode !== null || server.signalCode !== null) return;
    const exited = new Promise(resolve => server.once('exit', resolve));
    server.kill('SIGTERM'); await exited;
  };
  await start();
  browser = await chromium.launch();
  const context = await browser.newContext({ extraHTTPHeaders: { Authorization: `Bearer ${token}` }, locale: 'es-AR' });
  const page = await context.newPage();
  page.setDefaultTimeout(10000);
  console.log('Go server and browser started.');
  const errors = [], missing = new Set(), observed = new Set();
  page.on('pageerror', error => errors.push(error.message));
  page.on('response', response => { if (response.url().includes('/api/')) { observed.add(new URL(response.url()).pathname); if (response.status() >= 400) missing.add(`${response.status()} ${new URL(response.url()).pathname}`); } });
  await page.goto(`${base}/chains/person?lang=es`);
  await expect(page.getByRole('heading', { name: 'Personas', exact: true })).toBeVisible();
  for (const [query, name, status] of [['alice', 'Alice Morgan', 'Brecha'], ['bob', 'Bob Chen', 'Parcial'], ['eve', 'Eve Taylor', 'Por verificar']]) {
    await page.getByRole('searchbox', { name: 'Buscar persona por correo o nombre' }).fill(query);
    await page.getByRole('option', { name: new RegExp(name) }).click();
    await page.getByRole('group', { name: 'Herramientas de la persona' }).getByRole('button', { name: /^Netskope:/ }).click();
    await page.getByText('Evidencia del cliente Netskope', { exact: true }).click();
    await expect(page.getByLabel('Detalle de cobertura de la persona seleccionada')).toContainText(status);
  }
  for (const path of ['/api/auth/status', '/api/chains/person/search', '/api/chains/person/coverage/detail']) {
    if (!observed.has(path)) throw new Error(`No real HTTP response observed for ${path}`);
  }
  await page.screenshot({ path: join(tmpdir(), 'custodia-go-personas.png'), fullPage: true });
  const limited=await browser.newContext({extraHTTPHeaders:{Authorization:`Bearer ${limitedToken}`},locale:'es-AR'});
  const limitedPage=await limited.newPage();
  const limitedRequests=[];
  limitedPage.on('request',request=>limitedRequests.push(new URL(request.url()).pathname));
  await limitedPage.goto(`${base}/chains/person?lang=es`);
  await expect(limitedPage.getByRole('heading',{name:'Personas',exact:true})).toBeVisible();
  await limitedPage.getByRole('searchbox',{name:'Buscar persona por correo o nombre'}).fill('alice');
  await limitedPage.getByRole('option',{name:/Alice Morgan/}).click();
  const limitedSummary=await (await limited.request.get(`${base}/api/chains/person?email=alice.morgan@acme.example`)).json();
  if(!limitedSummary.tools.identity || limitedSummary.tools.netskope) throw new Error('Unreadable source appeared in person summary');
  await limitedPage.waitForLoadState('networkidle');
  if(limitedRequests.some(path=>path.includes('/coverage'))) throw new Error('Single-source browser requested a cross-source inference');
  for(const path of ['/api/chains/person/coverage','/api/chains/person/coverage/detail?email=alice.morgan@acme.example']){
    if((await limited.request.get(`${base}${path}`)).status()!==403) throw new Error('Single-source principal obtained cross-source evidence');
  }
  const summary = await (await context.request.get(`${base}/api/chains/person/coverage`)).json();
  if (summary.counts.denominator !== 7 || summary.counts.unknown !== 2 || summary.counts.devices_recent !== 4 || summary.counts.devices_total !== 5) throw new Error('Python denominator contract changed');
  const capture = summary.sources.netskope.completed_at;
  const preferences = await (await context.request.get(`${base}/api/workspaces/preferences`)).json();
  if (!preferences.recent_workspaces.includes('person')) throw new Error('Personas did not persist its recent workspace');
  await stop(); await start();
  const restarted = await (await context.request.get(`${base}/api/chains/person/coverage`)).json();
  if (restarted.sources.netskope.completed_at !== capture) throw new Error('Restart refreshed the evidence');
  const saved = await (await context.request.get(`${base}/api/workspaces/preferences`)).json();
  if (JSON.stringify(saved) !== JSON.stringify(preferences)) throw new Error('Preferences changed across restart');
  if((await limited.request.get(`${base}/api/chains/person/coverage`)).status()!==403) throw new Error('Restart lost the connection scope');
  execFileSync(binary,['revoke-lab','--dir',join(temporary,'lab'),'--subject','identity-reader'],{stdio:'pipe'});
  if((await limited.request.get(`${base}/api/auth/status`)).status()!==401) throw new Error('Revoked principal retained access');
  await limited.close();
  const denied = await fetch(`${base}/api/chains/person/search?q=Alice`, { headers: { 'X-Auth-Request-User': 'admin' } });
  if (denied.status !== 401) throw new Error('Forged identity header was accepted');
  if (errors.length || missing.size) throw new Error(JSON.stringify({ errors, missing: [...missing] }));
  console.log('Go + SQLite + real web: Alice/Bob/Eve, denominator, scoped users, restart, revocation and denied access OK.');
  await context.close();
  await stop();
} finally {
  await browser?.close();
  if (server?.exitCode === null && server.signalCode === null) {
    const exited = new Promise(resolve => server.once('exit', resolve));
    server.kill('SIGTERM'); await exited;
  }
  await rm(temporary, { recursive: true, force: true });
}
