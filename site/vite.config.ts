import { resolve } from 'node:path';
import { defineConfig } from 'vite';
import { pageFiles } from './site/content.mjs';

// Sitio de producto estático: la portada y las páginas de contenido se generan
// desde site/template.html, site/article.html y site/content.mjs por
// scripts/build-site.mjs. La demo es otra app (web/).
const input = Object.fromEntries(
  pageFiles.map((file, index) => [`page${index}`, resolve(import.meta.dirname, file)]),
);

export default defineConfig({
  build: {
    rollupOptions: { input },
  },
  server: { host: '127.0.0.1', port: 4186, strictPort: true },
  preview: { host: '127.0.0.1', port: 4186, strictPort: true },
});
