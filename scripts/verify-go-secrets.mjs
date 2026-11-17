// Connection-secret gate: crypto/store/API tests in Go, then the console
// flow in Chromium. The Go tests assert ciphertext at rest, AAD binding,
// write-only responses, rotation, retirement and the probe-gated schedule.
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
function run(command, args, options) {
  const result = spawnSync(command, args, { cwd: options.cwd, env: { ...process.env, ...options.env }, stdio: 'inherit' });
  if (result.status !== 0) process.exit(result.status ?? 1);
}

run('go', ['test', '-race', '-count=1', './internal/secrets', './internal/lab', '-run', 'TestConnectionSecret|TestConnectionManagement'], { cwd: resolve(root, 'server'), env: {} });
const port = await freePort();
run('npx', ['playwright', 'test', 'tests/e2e/cross/connection-secrets.spec.ts', '--project=chromium-dark'], { cwd: resolve(root, 'web'), env: { E2E_PORT: String(port) } });
console.log('Connection secrets + Go + SQLite + web: encryption at rest, write-only API, probe-gated schedule, rotation, pause and retirement OK.');
