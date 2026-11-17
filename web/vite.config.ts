import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { fileURLToPath } from 'node:url';
import { readdirSync, rmSync } from 'node:fs';
import { join, resolve } from 'node:path';

/* La app de Custodia. En desarrollo, /api va al backend local; con
   VITE_DEMO=1 la API simulada responde dentro del navegador (demo estática). */
const HOST_CONFIG = {
  host: '127.0.0.1' as const,
  port: 4184,
  proxy: {
    '/api': { target: 'http://127.0.0.1:8000', changeOrigin: true },
  },
};

const productionSources = fileURLToPath(new URL('./src/sources.core.ts', import.meta.url));
const productionStub = fileURLToPath(new URL('./src/cyberark/shared/production-stub.tsx', import.meta.url));
const excludedModules = [
  /\/sia-hub-workspace$/,
  /\/identity-workspace$/,
  /\/person-chains-workspace$/,
  /\/connection-inventory-workspace$/,
  /\/posture-workspace$/,
];

// Vendor marks of tools outside the MVP. Vite copies public/ whole, so the
// production build removes them; the demo keeps every mark it shows.
const excludedPublicAssets = /^(netskope|bigid)[-_.]/i;

/* The production profile excludes the DS Hub legacy tools from the graph.
   The demo keeps the full registry and the real lazy modules. */
function productionProfile() {
  let outDir = '';
  return {
    name: 'custodia-production-profile',
    enforce: 'pre' as const,
    configResolved(config: { root: string; build: { outDir: string } }) {
      outDir = resolve(config.root, config.build.outDir);
    },
    closeBundle() {
      if (process.env.VITE_DEMO === '1' || process.env.VITE_FULL === '1' || !outDir) return;
      for (const name of readdirSync(outDir)) {
        if (excludedPublicAssets.test(name)) rmSync(join(outDir, name), { force: true });
      }
    },
    resolveId(source: string) {
      if (process.env.VITE_DEMO === '1' || process.env.VITE_FULL === '1') return null;
      if (source.endsWith('/sources.ts') || source.endsWith('/sources')) return productionSources;
      if (excludedModules.some((pattern) => pattern.test(source))) return productionStub;
      return null;
    },
  };
}

export default defineConfig({
  plugins: [productionProfile(), react()],
  build: {
    // Advanced charts are already loaded on demand, but ECharts' optional
    // renderer/chart modules form one intentional lazy chunk (~588 kB).
    // Keep Vite's warning useful for the eagerly loaded application chunks.
    chunkSizeWarningLimit: 650,
  },
  server: HOST_CONFIG,
  preview: HOST_CONFIG,
});
