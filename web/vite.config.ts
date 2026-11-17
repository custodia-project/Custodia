import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { fileURLToPath } from 'node:url';

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

/* The production profile excludes the DS Hub legacy tools from the graph.
   The demo keeps the full registry and the real lazy modules. */
function productionProfile() {
  return {
    name: 'custodia-production-profile',
    enforce: 'pre' as const,
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
