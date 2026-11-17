import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

// Use Node for both tools so every build profile works in cmd, PowerShell
// and Unix shells without shell-specific environment assignments.
const args = process.argv.slice(2);
const env = { ...process.env };
if (args[0] === '--demo' || args[0] === '--all') {
  const profile = args.shift();
  env.VITE_DEMO = profile === '--demo' ? '1' : '0';
  env.VITE_FULL = profile === '--all' ? '1' : '0';
}

for (const [entry, toolArgs] of [
  ['../node_modules/typescript/bin/tsc', ['--noEmit']],
  ['../node_modules/vite/bin/vite.js', ['build', ...args]],
]) {
  const result = spawnSync(process.execPath, [fileURLToPath(new URL(entry, import.meta.url)), ...toolArgs], {
    env,
    stdio: 'inherit',
  });
  if (result.error) console.error(result.error.message);
  if (result.status !== 0) process.exit(result.status ?? 1);
}
