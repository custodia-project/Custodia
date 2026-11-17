// Wazuh gate: adapter/probe/store tests in Go, then the console wiring in
// Chromium. The real 4.14.10 acceptance is documented in CONNECTORS.md; this
// gate stays hermetic and does not require Docker.
import { spawnSync } from 'node:child_process';
import { createServer } from 'node:net';
import { resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
function freePort() {
  return new Promise((done) => {
    const server = createServer();
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      server.close(() => done(port));
    });
  });
}
function run(command, args, cwd) {
  const result = spawnSync(command, args, { cwd, stdio: 'inherit' });
  if (result.status !== 0) process.exit(result.status ?? 1);
}

run('go', ['test', '-race', '-count=1', './internal/adapters/wazuh', './internal/probe', './internal/lab', '-run', 'TestWazuh|TestBasicExchange'], resolve(root, 'server'));
const port = await freePort();
process.env.E2E_PORT = String(port);
run('npx', ['playwright', 'test', 'tests/e2e/cross/wazuh.spec.ts', '--project=chromium-dark'], resolve(root, 'web'));
console.log('Wazuh + Go + web: paginated agents/groups, basic-exchange, declared metrics and console wiring OK.');
