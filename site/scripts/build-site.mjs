// Genera las páginas del sitio de producto desde una plantilla y sus textos:
// la portada (inglés y español) y las páginas de inventario, conectores,
// CyberArk PAM, Wazuh, instalación y límites. Corre antes de Vite; el sitemap
// se genera después del build desde la misma lista de páginas.
//
// Una clave sin texto detiene el build, así ningún idioma queda a medias.
import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { articles, articleKeys, base, demoBase, nav, reviewed, siblingOf, ui } from '../site/content.mjs';
import { pages as homePages } from '../site/strings.mjs';
import { verification } from '../site/verification.mjs';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const homeTemplate = await readFile(join(root, 'site/template.html'), 'utf8');
const articleTemplate = await readFile(join(root, 'site/article.html'), 'utf8');
const header = await readFile(join(root, 'site/partials/header.html'), 'utf8');
const footer = await readFile(join(root, 'site/partials/footer.html'), 'utf8');

// Las capturas no llevan hash en el nombre: la versión en la URL cambia con su
// contenido, así una captura nueva no queda escondida detrás de la caché.
const shots = createHash('sha256');
// Una carpeta por idioma (shots/en, shots/es), cada captura en PNG y WebP
// (el WebP pesa ~5 veces menos); todo entra en la versión.
for (const lang of ['en', 'es']) {
  for (const name of ['summary', 'person', 'tools', 'people', 'metrics', 'safes', 'inventory', 'posture']) {
    for (const ext of ['png', 'webp']) shots.update(await readFile(join(root, `public/shots/${lang}/${name}.${ext}`)));
  }
}
const shotVersion = shots.digest('hex').slice(0, 10);
const demo = (process.env.CUSTODIA_DEMO_URL || demoBase).replace(/\/$/, '');
const homeHref = (lang) => (lang === 'en' ? '/' : '/es/');
const ogLocale = (lang) => (lang === 'en' ? 'en_US' : 'es_ES');
const escapeAttr = (value) => String(value)
  .replace(/&/g, '&amp;')
  .replace(/"/g, '&quot;')
  .replace(/</g, '&lt;')
  .replace(/>/g, '&gt;');
const verificationMeta = (name, token) => {
  const value = String(token ?? '').trim();
  return value ? `<meta name="${name}" content="${escapeAttr(value)}" />` : '';
};

function fill(template, values, label) {
  const withPartials = template.replace('{{header}}', header).replace('{{footer}}', footer);
  const missing = new Set();
  const html = withPartials.replace(/\{\{(\w+)\}\}/g, (_, key) => {
    if (!(key in values)) missing.add(key);
    return values[key] ?? '';
  });
  if (missing.size) throw new Error(`${label}: faltan textos: ${[...missing].join(', ')}`);
  return html;
}

function entities(lang) {
  return [
    {
      '@type': 'WebSite',
      '@id': `${base}/#website`,
      url: `${base}/`,
      name: 'Custodia',
      description: ui[lang].entityDescription,
      inLanguage: ['en', 'es'],
    },
    {
      '@type': 'SoftwareApplication',
      '@id': `${base}/#software`,
      name: 'Custodia',
      url: `${base}/`,
      description: ui[lang].entityDescription,
      applicationCategory: 'SecurityApplication',
      applicationSubCategory: 'Security tool inventory and metrics',
      operatingSystem: 'Linux',
      isAccessibleForFree: true,
      license: 'https://www.apache.org/licenses/LICENSE-2.0',
      sameAs: ['https://github.com/custodia-project/Custodia'],
      featureList: ui[lang].features,
    },
  ];
}

function jsonLd({ lang, page, isHome, breadcrumbs }) {
  const pageUrl = `${base}${page.path}`;
  const graph = entities(lang);
  if (!isHome) {
    graph.push({
      '@type': 'TechArticle',
      '@id': `${pageUrl}#article`,
      headline: page.headline,
      description: page.description,
      inLanguage: lang,
      dateModified: reviewed,
      mainEntityOfPage: pageUrl,
      isPartOf: { '@id': `${base}/#website` },
      about: { '@id': `${base}/#software` },
    });
    graph.push({
      '@type': 'BreadcrumbList',
      '@id': `${pageUrl}#breadcrumb`,
      itemListElement: breadcrumbs.map((item, index) => ({
        '@type': 'ListItem',
        position: index + 1,
        name: item.name,
        item: `${base}${item.path}`,
      })),
    });
  }
  return JSON.stringify({ '@context': 'https://schema.org', '@graph': graph }, null, 2).replace(/</g, '\\u003c');
}

function commonValues(lang, page, other) {
  return {
    ...ui[lang],
    ...nav[lang],
    lang,
    title: page.title,
    description: page.description,
    path: page.path,
    homeHref: homeHref(lang),
    otherHref: other.path,
    otherLang: other.lang,
    otherCode: ui[lang].otherCode,
    ogLocale: ogLocale(lang),
    ogImageAlt: page.title,
    demo,
    demoLang: `?lang=${lang}`,
    reviewed,
    shotVersion,
    googleVerification: verificationMeta(
      'google-site-verification',
      process.env.GOOGLE_SITE_VERIFICATION || verification.google,
    ),
    bingVerification: verificationMeta(
      'msvalidate.01',
      process.env.BING_SITE_VERIFICATION || verification.bing,
    ),
  };
}

function breadcrumbsFor(key, page, lang) {
  const trail = [{ name: ui[lang].homeCrumb, path: homeHref(lang) }];
  if (key === 'cyberark-pam' || key === 'wazuh') {
    trail.push({ name: ui[lang].navConnectors, path: nav[lang].connectorsHref });
  }
  trail.push({ name: page.crumb, path: page.path });
  return trail;
}

const renderStatus = (status) => status.map(({ tone, label }) => `<li><span class="tag is-${tone}">${label}</span></li>`).join('');
const renderBreadcrumbs = (trail) => trail.map((item, index) => (
  index === trail.length - 1
    ? `<li><span aria-current="page">${item.name}</span></li>`
    : `<li><a href="${item.path}">${item.name}</a></li>`
)).join('');
const renderDocs = (docs) => docs.map((doc) => `<li><a href="${doc.href}">${doc.label}</a></li>`).join('');
const renderRelated = (related) => related.map((link) => `<li><a href="${link.href}">${link.label} <span aria-hidden="true">→</span></a></li>`).join('');

for (const [lang, strings] of Object.entries(homePages)) {
  const page = { path: strings.path, title: strings.title, description: strings.description };
  const other = siblingOf({ key: 'home', lang, isHome: true });
  const values = {
    ...commonValues(lang, page, other),
    ...strings,
    jsonLd: jsonLd({ lang, page, isHome: true }),
  };
  const html = fill(homeTemplate, values, lang);
  const out = join(root, strings.path === '/' ? 'index.html' : `${strings.path.slice(1)}index.html`);
  await mkdir(dirname(out), { recursive: true });
  await writeFile(out, html.replace('<!doctype html>\n', '<!doctype html>\n<!-- Generado por scripts/build-site.mjs desde site/template.html. No editar. -->\n'));
}

for (const key of articleKeys) {
  const enPath = articles.en[key].path;
  const esPath = articles.es[key].path;
  for (const lang of ['en', 'es']) {
    const page = articles[lang][key];
    const other = siblingOf({ key, lang, isHome: false });
    const breadcrumbs = breadcrumbsFor(key, page, lang);
    const values = {
      ...commonValues(lang, page, other),
      ...page,
      enPath,
      esPath,
      status: renderStatus(page.status),
      breadcrumbs: renderBreadcrumbs(breadcrumbs),
      docs: renderDocs(page.docs),
      related: renderRelated(page.related),
      jsonLd: jsonLd({ lang, page, isHome: false, breadcrumbs }),
    };
    const html = fill(articleTemplate, values, `${key}/${lang}`);
    const out = join(root, page.path === '/' ? 'index.html' : `${page.path.slice(1)}index.html`);
    await mkdir(dirname(out), { recursive: true });
    await writeFile(out, html.replace('<!doctype html>\n', '<!doctype html>\n<!-- Generado por scripts/build-site.mjs desde site/article.html. No editar. -->\n'));
  }
}
