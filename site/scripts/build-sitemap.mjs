// Genera dist/sitemap.xml después de Vite. La lista de páginas y sus pares
// en/es viven en site/content.mjs, así que una página nueva no puede quedar
// fuera del sitemap ni declarar alternates distintos de su canonical.
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { allPages, base } from '../site/content.mjs';

const dist = resolve(import.meta.dirname, '..', 'dist');
const groups = new Map();
for (const page of allPages) {
  const group = groups.get(page.key) ?? [];
  group.push(page);
  groups.set(page.key, group);
}

const entries = [];
for (const group of groups.values()) {
  const en = group.find((page) => page.lang === 'en');
  const es = group.find((page) => page.lang === 'es');
  if (!en || !es) throw new Error(`la página ${group[0].key} no tiene par en/es`);
  const alternates = [
    `    <xhtml:link rel="alternate" hreflang="en" href="${base}${en.path}" />`,
    `    <xhtml:link rel="alternate" hreflang="es" href="${base}${es.path}" />`,
    `    <xhtml:link rel="alternate" hreflang="x-default" href="${base}${en.path}" />`,
  ].join('\n');
  for (const page of group) {
    entries.push(`  <url>\n    <loc>${base}${page.path}</loc>\n${alternates}\n  </url>`);
  }
}

const xml = `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"
        xmlns:xhtml="http://www.w3.org/1999/xhtml">
${entries.join('\n')}
</urlset>
`;
await mkdir(dist, { recursive: true });
await writeFile(resolve(dist, 'sitemap.xml'), xml);
process.stdout.write(`Sitemap: ${entries.length} URLs.\n`);
