// Real frontend + Go + disposable SQLite. No API interception or mock responses.
import { spawn, execFileSync } from 'node:child_process';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer as createNetServer } from 'node:net';
import { createServer as createSyncServer } from 'node:http';
import { chromium, expect } from '../web/node_modules/@playwright/test/index.mjs';

const root = resolve(import.meta.dirname, '..');
const temporary = await mkdtemp(join(tmpdir(), 'custodia-go-web-'));
const binary = join(temporary, 'custodia');
const webBuild = join(temporary, 'web');
let server, browser, syncServer;
try {
  execFileSync('npm', ['--prefix', 'web', 'run', 'build', '--', '--outDir', webBuild], { cwd: root, env: { ...process.env, VITE_FULL: '1' }, stdio: 'inherit' });
  execFileSync('go', ['build', '-o', binary, './cmd/custodia'], { cwd: join(root, 'server'), env: { ...process.env, CGO_ENABLED: '0' }, stdio: 'inherit' });
  execFileSync(binary, ['init-lab', '--dir', join(temporary, 'lab')], { stdio: 'pipe' });
  const token = await readFile(join(temporary, 'lab/lab.token'), 'utf8');
  const limitedFile=join(temporary,'identity.token');
  execFileSync(binary,['issue-lab','--dir',join(temporary,'lab'),'--subject','identity-reader','--connections','acme-identity','--out',limitedFile],{stdio:'pipe'});
  const limitedToken=await readFile(limitedFile,'utf8');
  const syncPages = {
    '': { objects: [
      { external_id: 'sync-u1', attributes: { email: 'alice@acme.example', name: 'Alice' } },
      { external_id: 'sync-u2', attributes: { email: 'bob@acme.example', name: 'Bob' } },
    ], next_cursor: 'page-2' },
    'page-2': { objects: [
      { external_id: 'sync-u3', attributes: { email: 'carol@acme.example', name: 'Carol' } },
    ], next_cursor: '' },
  };
  const safePages = {
    '': { value: [
      { safeNumber: 9001, safeName: 'AlphaSafe', safeUrlId: 'alpha-safe', location: 'Acme DC', creatorName: 'Alice Admin', olacEnabled: true, numberOfVersionsRetention: 5, numberOfDaysRetention: 30, autoPurgeEnabled: false, creationTime: 1767323045, lastModificationTime: 1767409445 },
      { safeNumber: 9002, safeName: 'BetaSafe', safeUrlId: 'beta-safe', location: 'Acme DC', creatorName: 'Bob Admin', olacEnabled: false, numberOfVersionsRetention: 0, numberOfDaysRetention: 0, autoPurgeEnabled: true, creationTime: 1767323045, lastModificationTime: 1767409445 },
    ], count: 3, nextLink: '/API/Safes?limit=2&offset=2' },
    '2': { value: [
      { safeNumber: 9003, safeName: 'GammaSafe', safeUrlId: 'gamma-safe', location: 'Acme DC', creatorName: 'Carol Admin', olacEnabled: true, numberOfVersionsRetention: 5, numberOfDaysRetention: 30, autoPurgeEnabled: false, creationTime: 1767323045, lastModificationTime: 1767409445 },
    ], count: 3, nextLink: '' },
  };
  const memberPages = {
    AlphaSafe: {
      '': { value: [
        { safeNumber: 9001, memberId: 'alpha-user', memberName: 'Alice', memberType: 'User', membershipExpirationDate: 1767323045, isExpiredMembershipEnable: false, isPredefinedUser: false, isReadOnly: false, permissions: { useAccounts: true } },
        { safeNumber: 9001, memberId: 'alpha-group', memberName: 'Group A', memberType: 'Group', membershipExpirationDate: 1767323045, isExpiredMembershipEnable: false, isPredefinedUser: true, isReadOnly: true, permissions: { retrieveAccounts: true } },
      ], count: 3, nextLink: '/API/Safes/AlphaSafe/Members?limit=2&offset=2' },
      '2': { value: [
        { safeNumber: 9001, memberId: 'alpha-auditor', memberName: 'Bob', memberType: 'User', membershipExpirationDate: 1767323045, isExpiredMembershipEnable: false, isPredefinedUser: false, isReadOnly: false, permissions: { listAccounts: true } },
      ], count: 3, nextLink: '' },
    },
    BetaSafe: {
      '': { value: [
        { safeNumber: 9002, memberId: 'beta-user', memberName: 'Carol', memberType: 'User', membershipExpirationDate: 1767323045, isExpiredMembershipEnable: false, isPredefinedUser: false, isReadOnly: false, permissions: {} },
      ], count: 1, nextLink: '' },
    },
    GammaSafe: {
      '': { value: [], count: 0, nextLink: '' },
    },
  };
  const accountPages = {
    '': { value: [
      { id: 'acct-alpha', name: 'svc-alpha', userName: 'svc-alpha', address: 'db.acme.example', safeName: 'AlphaSafe', platformId: 'Unix', accountType: 'Service', category: 'DB', secretManagement: { automaticManagementEnabled: true, status: 'Success', lastModifiedTime: 1767323045 }, secret: 'never stored', platformAccountProperties: { port: '5432' }, manualManagementReason: 'local' },
      { id: 'acct-beta', name: 'svc-beta', userName: 'svc-beta', address: 'app.acme.example', safeName: 'BetaSafe', platformId: 'Windows', accountType: 'Service', category: 'APP', secretManagement: { automaticManagementEnabled: false, status: 'failure', lastModifiedTime: 1767409445 } },
    ], count: 3, nextLink: '/API/Accounts?limit=2&offset=2' },
    '2': { value: [
      { id: 'acct-audit', name: 'svc-audit', userName: 'svc-audit', address: 'audit.acme.example', safeName: 'AlphaSafe', platformId: 'Unix', accountType: 'Service', category: 'DB', secretManagement: { automaticManagementEnabled: true, status: 'in_process' } },
    ], count: 3, nextLink: '' },
  };
  const platformResponse = {
    Platforms: [
      { general: { id: 'WinDomain', name: 'Windows Domain', active: true, platformBaseID: 'WinDomain', platformType: 'Regular', systemType: 'Windows' }, sessionManagement: { PSMServerID: 'PSMServer_acme' }, properties: { secret: 'never stored' } },
      { general: { id: 'Unix', name: 'Unix', active: false, platformBaseID: 'Unix', platformType: 'Regular', systemType: 'Unix' }, sessionManagement: { PSMServerID: 'PSMSERVER_ACME' } },
      { general: { id: 'Database', name: 'Database', active: true, platformBaseID: 'Database', platformType: 'Regular', systemType: 'Database' }, sessionManagement: { PSMServerID: 'PSMServer_missing' } },
    ],
    Total: 3,
  };
  const psmServerResponse = {
    PSMServers: [
      { ID: 'PSMServer_acme', Name: 'PSM Acme', Address: 'psm.acme.example', Properties: { secret: 'never stored' } },
      { ID: 'PSMServer_secondary', Name: 'PSM Secondary', Address: 'psm2.acme.example' },
      { ID: 'PSMServer_unnamed' },
    ],
    Total: 3,
  };
  const psmConnectorResponse = {
    PSMConnectors: [
      { ID: 'PSM-RDP', DisplayName: 'RDP', Type: 'session', secret: 'never stored' },
      { ID: 'PSM-SSH', DisplayName: 'SSH' },
    ],
    Total: 2,
  };
  syncServer = createSyncServer((request, response) => {
    const url = new URL(request.url, 'http://127.0.0.1');
    let body;
    if (url.pathname === '/v1/collections/users') {
      const cursor = url.searchParams.get('cursor') || '';
      if (!(cursor in syncPages)) { response.writeHead(400).end(); return; }
      body = JSON.stringify(syncPages[cursor]);
    } else if (url.pathname === '/API/Safes') {
      const offset = url.searchParams.get('offset') || '';
      if (!(offset in safePages)) { response.writeHead(400).end(); return; }
      body = JSON.stringify(safePages[offset]);
    } else if (url.pathname === '/API/Accounts') {
      const offset = url.searchParams.get('offset') || '';
      if (!(offset in accountPages)) { response.writeHead(400).end(); return; }
      body = JSON.stringify(accountPages[offset]);
    } else if (url.pathname === '/API/Platforms') {
      body = JSON.stringify(platformResponse);
    } else if (url.pathname === '/API/PSM/Servers') {
      body = JSON.stringify(psmServerResponse);
    } else if (url.pathname === '/API/PSM/Connectors') {
      body = JSON.stringify(psmConnectorResponse);
    } else if (url.pathname.startsWith('/API/Safes/') && url.pathname.endsWith('/Members')) {
      const safeName = decodeURIComponent(url.pathname.slice('/API/Safes/'.length, -'/Members'.length));
      const offset = url.searchParams.get('offset') || '';
      const pages = memberPages[safeName];
      if (!pages || !(offset in pages)) { response.writeHead(400).end(); return; }
      body = JSON.stringify(pages[offset]);
    } else {
      response.writeHead(404).end();
      return;
    }
    response.writeHead(200, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) });
    response.end(body);
  });
  await new Promise(resolve => syncServer.listen(0, '127.0.0.1', resolve));
  execFileSync(binary, ['add-http-lab', '--dir', join(temporary, 'lab'), '--connection', 'lab-http-a', '--url', `http://127.0.0.1:${syncServer.address().port}`, '--collection', 'users', '--scope', 'synthetic-users-v1', '--interval-minutes', '0', '--grant', 'synthetic-lab'], { stdio: 'pipe' });
  execFileSync(binary, ['add-http-lab', '--dir', join(temporary, 'lab'), '--connection', 'pam-safes', '--connector', 'cyberark-pam-safes-v1', '--url', `http://127.0.0.1:${syncServer.address().port}`, '--collection', 'safes', '--scope', 'synthetic-safes-v1', '--generation', 'cyberark-pam-safes-v1', '--page-size', '2', '--interval-minutes', '0', '--grant', 'synthetic-lab'], { stdio: 'pipe' });
  execFileSync(binary, ['add-http-lab', '--dir', join(temporary, 'lab'), '--connection', 'pam-members', '--connector', 'cyberark-pam-safes-v1', '--url', `http://127.0.0.1:${syncServer.address().port}`, '--collection', 'safe_members', '--scope', 'synthetic-members-v1', '--generation', 'cyberark-pam-safes-v1', '--page-size', '2', '--interval-minutes', '0', '--grant', 'synthetic-lab'], { stdio: 'pipe' });
  execFileSync(binary, ['add-http-lab', '--dir', join(temporary, 'lab'), '--connection', 'pam-accounts', '--connector', 'cyberark-pam-safes-v1', '--url', `http://127.0.0.1:${syncServer.address().port}`, '--collection', 'accounts', '--scope', 'synthetic-accounts-v1', '--generation', 'cyberark-pam-safes-v1', '--page-size', '2', '--interval-minutes', '0', '--grant', 'synthetic-lab'], { stdio: 'pipe' });
  execFileSync(binary, ['add-http-lab', '--dir', join(temporary, 'lab'), '--connection', 'pam-platforms', '--connector', 'cyberark-pam-safes-v1', '--url', `http://127.0.0.1:${syncServer.address().port}`, '--collection', 'platforms', '--scope', 'synthetic-platforms-v1', '--generation', 'cyberark-pam-safes-v1', '--page-size', '2', '--interval-minutes', '0', '--grant', 'synthetic-lab'], { stdio: 'pipe' });
  execFileSync(binary, ['add-http-lab', '--dir', join(temporary, 'lab'), '--connection', 'pam-psm-servers', '--connector', 'cyberark-pam-safes-v1', '--url', `http://127.0.0.1:${syncServer.address().port}`, '--collection', 'psm_servers', '--scope', 'synthetic-psm-servers-v1', '--generation', 'cyberark-pam-safes-v1', '--page-size', '2', '--interval-minutes', '0', '--grant', 'synthetic-lab'], { stdio: 'pipe' });
  execFileSync(binary, ['add-http-lab', '--dir', join(temporary, 'lab'), '--connection', 'pam-psm-connectors', '--connector', 'cyberark-pam-safes-v1', '--url', `http://127.0.0.1:${syncServer.address().port}`, '--collection', 'psm_connectors', '--scope', 'synthetic-psm-connectors-v1', '--generation', 'cyberark-pam-safes-v1', '--page-size', '1', '--interval-minutes', '0', '--grant', 'synthetic-lab'], { stdio: 'pipe' });
  execFileSync(binary, ['add-http-lab', '--dir', join(temporary, 'lab'), '--connection', 'pam-psm-links', '--connector', 'cyberark-pam-safes-v1', '--url', `http://127.0.0.1:${syncServer.address().port}`, '--collection', 'platform_psm_links', '--scope', 'synthetic-psm-links-v1', '--generation', 'cyberark-pam-safes-v1', '--page-size', '2', '--interval-minutes', '0', '--grant', 'synthetic-lab'], { stdio: 'pipe' });
  const probe = createNetServer();
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
  const waitIngestionRun = async (runId) => {
    for (let attempt = 0; attempt < 100; attempt += 1) {
      const run = await (await context.request.get(`${base}/api/ingestion/runs/${runId}`)).json();
      if (!['queued', 'running'].includes(run.status)) return run;
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    throw new Error(`Ingestion run ${runId} did not finish`);
  };
  const auth = await (await context.request.get(`${base}/api/auth/status`)).json();
  if (!auth.permissions.includes('ingestion:run')) throw new Error('Run capability missing from auth status');
  const firstSync = await context.request.post(`${base}/api/ingestion/schedules/lab-http-a/runs`, { data: {} });
  if (firstSync.status() !== 202) throw new Error(`Sync start returned ${firstSync.status()}`);
  const firstRun = await waitIngestionRun((await firstSync.json()).run_id);
  if (firstRun.status !== 'completed' || firstRun.pages !== 2 || firstRun.received_object_count !== 3) throw new Error('Pagination/worker contract changed');
  const records = await (await context.request.get(`${base}/api/ingestion/connections/lab-http-a/records`)).json();
  if (records.rows.length !== 3 || !records.rows.every(record => record.last_run_id === firstRun.run_id)) throw new Error('Synced records or provenance missing');
  const pamSync = await context.request.post(`${base}/api/ingestion/schedules/pam-safes/runs`, { data: {} });
  if (pamSync.status() !== 202) throw new Error(`PAM sync start returned ${pamSync.status()}`);
  const pamRun = await waitIngestionRun((await pamSync.json()).run_id);
  if (pamRun.status !== 'completed' || pamRun.source_system !== 'cyberark' || pamRun.pages !== 2 || pamRun.received_object_count !== 3) throw new Error('PAM Safes pagination/worker contract changed');
  const safes = await (await context.request.get(`${base}/api/ingestion/connections/pam-safes/records`)).json();
  if (safes.rows.length !== 3 || !safes.rows.every(record => record.last_run_id === pamRun.run_id) || safes.rows[0].attributes.safe_url_id !== 'alpha-safe' || 'secret' in safes.rows[0].attributes) throw new Error('PAM Safes records or allowlist missing');
  const memberSync = await context.request.post(`${base}/api/ingestion/schedules/pam-members/runs`, { data: {} });
  if (memberSync.status() !== 202) throw new Error(`PAM member sync start returned ${memberSync.status()}`);
  const memberRun = await waitIngestionRun((await memberSync.json()).run_id);
  if (memberRun.status !== 'completed' || memberRun.source_system !== 'cyberark' || memberRun.collection !== 'safe_members' || memberRun.pages !== 4 || memberRun.received_object_count !== 4 || memberRun.received_member_count !== 4) throw new Error('PAM Safe members pagination/worker contract changed');
  const members = await (await context.request.get(`${base}/api/ingestion/connections/pam-members/records`)).json();
  if (members.rows.length !== 4 || !members.rows.every(record => record.last_run_id === memberRun.run_id) || members.rows[0].attributes.member_id !== 'alpha-user' || !members.rows[0].attributes.permissions.includes('useAccounts') || 'secret' in members.rows[0].attributes) throw new Error('PAM Safe member records or allowlist missing');
  const accountSync = await context.request.post(`${base}/api/ingestion/schedules/pam-accounts/runs`, { data: {} });
  if (accountSync.status() !== 202) throw new Error(`PAM account sync start returned ${accountSync.status()}`);
  const accountRun = await waitIngestionRun((await accountSync.json()).run_id);
  if (accountRun.status !== 'completed' || accountRun.source_system !== 'cyberark' || accountRun.collection !== 'accounts' || accountRun.pages !== 2 || accountRun.received_object_count !== 3) throw new Error('PAM account pagination/worker contract changed');
  const accounts = await (await context.request.get(`${base}/api/ingestion/connections/pam-accounts/records`)).json();
  if (accounts.rows.length !== 3 || !accounts.rows.every(record => record.last_run_id === accountRun.run_id) || accounts.rows[0].attributes.account_id !== 'acct-alpha' || accounts.rows[0].attributes.management_status !== 'success' || 'secret' in accounts.rows[0].attributes || 'platformAccountProperties' in accounts.rows[0].attributes) throw new Error('PAM account records or allowlist missing');
  const platformSync = await context.request.post(`${base}/api/ingestion/schedules/pam-platforms/runs`, { data: {} });
  if (platformSync.status() !== 202) throw new Error(`PAM platform sync start returned ${platformSync.status()}`);
  const platformRun = await waitIngestionRun((await platformSync.json()).run_id);
  if (platformRun.status !== 'completed' || platformRun.source_system !== 'cyberark' || platformRun.collection !== 'platforms' || platformRun.pages !== 2 || platformRun.received_object_count !== 3) throw new Error('PAM platform pagination/worker contract changed');
  const platforms = await (await context.request.get(`${base}/api/ingestion/connections/pam-platforms/records`)).json();
  if (platforms.rows.length !== 3 || !platforms.rows.every(record => record.last_run_id === platformRun.run_id) || platforms.rows[0].attributes.platform_name !== 'Windows Domain' || platforms.rows[0].attributes.psm_server_id !== 'PSMServer_acme' || 'properties' in platforms.rows[0].attributes || 'secret' in platforms.rows[0].attributes) throw new Error('PAM platform records or allowlist missing');
  const psmServerSync = await context.request.post(`${base}/api/ingestion/schedules/pam-psm-servers/runs`, { data: {} });
  if (psmServerSync.status() !== 202) throw new Error(`PSM server sync start returned ${psmServerSync.status()}`);
  const psmServerRun = await waitIngestionRun((await psmServerSync.json()).run_id);
  if (psmServerRun.status !== 'completed' || psmServerRun.collection !== 'psm_servers' || psmServerRun.pages !== 2 || psmServerRun.received_object_count !== 3) throw new Error('PSM server pagination/worker contract changed');
  const psmServers = await (await context.request.get(`${base}/api/ingestion/connections/pam-psm-servers/records`)).json();
  if (psmServers.rows.length !== 3 || !psmServers.rows.every(record => record.last_run_id === psmServerRun.run_id) || psmServers.rows[0].attributes.psm_name !== 'PSM Acme' || 'Properties' in psmServers.rows[0].attributes || 'secret' in psmServers.rows[0].attributes) throw new Error('PSM server records or allowlist missing');
  const psmConnectorSync = await context.request.post(`${base}/api/ingestion/schedules/pam-psm-connectors/runs`, { data: {} });
  if (psmConnectorSync.status() !== 202) throw new Error(`PSM connector sync start returned ${psmConnectorSync.status()}`);
  const psmConnectorRun = await waitIngestionRun((await psmConnectorSync.json()).run_id);
  if (psmConnectorRun.status !== 'completed' || psmConnectorRun.collection !== 'psm_connectors' || psmConnectorRun.pages !== 2 || psmConnectorRun.received_object_count !== 2) throw new Error('PSM connector pagination/worker contract changed');
  const psmConnectors = await (await context.request.get(`${base}/api/ingestion/connections/pam-psm-connectors/records`)).json();
  if (psmConnectors.rows.length !== 2 || !psmConnectors.rows.every(record => record.last_run_id === psmConnectorRun.run_id) || psmConnectors.rows[0].attributes.connector_name !== 'RDP' || 'Type' in psmConnectors.rows[0].attributes || 'secret' in psmConnectors.rows[0].attributes) throw new Error('PSM connector records or allowlist missing');
  const psmLinkSync = await context.request.post(`${base}/api/ingestion/schedules/pam-psm-links/runs`, { data: {} });
  if (psmLinkSync.status() !== 202) throw new Error(`PSM link sync start returned ${psmLinkSync.status()}`);
  const psmLinkRun = await waitIngestionRun((await psmLinkSync.json()).run_id);
  if (psmLinkRun.status !== 'completed' || psmLinkRun.collection !== 'platform_psm_links' || psmLinkRun.pages !== 2 || psmLinkRun.received_object_count !== 3) throw new Error('platform↔PSM link pagination/worker contract changed');
  const psmLinks = await (await context.request.get(`${base}/api/ingestion/connections/pam-psm-links/records`)).json();
  if (psmLinks.rows.length !== 3 || !psmLinks.rows.every(record => record.last_run_id === psmLinkRun.run_id) || !psmLinks.rows.some(record => record.attributes.resolution === 'exact') || !psmLinks.rows.some(record => record.attributes.resolution === 'casefold') || !psmLinks.rows.some(record => record.attributes.resolution === 'missing') || psmLinks.rows.some(record => 'secret' in record.attributes)) throw new Error('platform↔PSM link evidence missing');
  const allSync = await context.request.post(`${base}/api/ingestion/all/runs?scope=all`, { data: { max_safes: 0, page_size: 100, member_delay_seconds: 0.3, reconcile_complete: true } });
  if (allSync.status() !== 202) throw new Error(`All-run start returned ${allSync.status()}`);
  const batch = await allSync.json();
  if (batch.runs.length !== 8 || new Set(batch.runs.map(run => run.source_system)).size !== 2) throw new Error(`Expected eight runs over two source systems, got ${batch.runs.map(run => run.source_system).join(',')}`);
  for (const queued of batch.runs) {
    if ((await waitIngestionRun(queued.run_id)).status !== 'completed') throw new Error('UI batch run failed');
  }
  const cybarkSync = await context.request.post(`${base}/api/ingestion/all/runs?scope=cyberark`, { data: {} });
  if (cybarkSync.status() !== 202) throw new Error(`CyberArk batch start returned ${cybarkSync.status()}`);
  const cybarkBatch = await cybarkSync.json();
  if (cybarkBatch.runs.length !== 7 || cybarkBatch.runs.some(run => run.source_system !== 'cyberark') || new Set(cybarkBatch.runs.map(run => run.task_key)).size !== 7) throw new Error('CyberArk scope did not isolate all PAM sources');
  for (const queued of cybarkBatch.runs) {
    if ((await waitIngestionRun(queued.run_id)).status !== 'completed') throw new Error('CyberArk batch run failed');
  }
  const scheduleUpdate = await context.request.put(`${base}/api/ingestion/schedules/lab-http-a`, { data: { interval_minutes: 5 } });
  if (scheduleUpdate.status() !== 200) throw new Error(`Schedule update returned ${scheduleUpdate.status()}`);
  const schedules = await (await context.request.get(`${base}/api/ingestion/schedules`)).json();
  const schedule = schedules.rows.find(row => row.task_key === 'lab-http-a');
  if (!schedule || schedule.interval_minutes !== 5 || schedule.interval_source !== 'administrative') throw new Error('Schedule did not persist');
  const history = await (await context.request.get(`${base}/api/ingestion/sources/netskope/stages/history?runs=5`)).json();
  if (!history.rows.length || history.rows[0].stage !== 'extract') throw new Error('Stage history missing');
  const pamHistory = await (await context.request.get(`${base}/api/ingestion/sources/cyberark/stages/history?runs=5`)).json();
  if (!pamHistory.rows.length || pamHistory.rows[0].stage !== 'extract' || pamHistory.rows.some(row => history.rows.some(other => other.run_id === row.run_id))) throw new Error('CyberArk stage history missing or mixed with Netskope');
  // Exercise the real UI refresh path: menu -> Actualizar datos -> polling -> toast.
  await page.getByRole('button', { name: 'Abrir menú de usuario', exact: true }).click();
  await page.getByRole('button', { name: /Actualizar datos/ }).click();
  await expect(page.getByText('Sincronización completada', { exact: true })).toBeVisible();
  if (!observed.has('/api/ingestion/all/runs') || !observed.has('/api/ingestion/runs')) throw new Error('UI sync did not use the ingestion API');
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
  console.log('Go + SQLite + real web: Alice/Bob/Eve, denominator, scoped users, sync worker/UI with Netskope fixture, CyberArk PAM Safes, Safe members, accounts, platforms, PSM servers/connectors and platform↔PSM links, restart, revocation and denied access OK.');
  await context.close();
  await stop();
} finally {
  await browser?.close();
  if (server?.exitCode === null && server.signalCode === null) {
    const exited = new Promise(resolve => server.once('exit', resolve));
    server.kill('SIGTERM'); await exited;
  }
  if (syncServer) await new Promise(resolve => syncServer.close(resolve));
  await rm(temporary, { recursive: true, force: true });
}
