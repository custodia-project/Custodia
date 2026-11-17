// Registro de páginas del sitio. Una página por idioma comparte "key" con su
// par para que canonical, hreflang y sitemap salgan de la misma lista.
import { articlesEn } from './articles-en.mjs';
import { articlesEs } from './articles-es.mjs';

export const base = 'https://custodia.octaviosantacruz.com';
export const demoBase = 'https://custodia-demo.pages.dev';
export const reviewed = '2026-11-17';

export const nav = {
  en: {
    inventoryHref: '/inventory/',
    connectorsHref: '/connectors/',
    installHref: '/install/',
    limitsHref: '/limits/',
  },
  es: {
    inventoryHref: '/es/inventario/',
    connectorsHref: '/es/conectores/',
    installHref: '/es/instalar/',
    limitsHref: '/es/limites/',
  },
};

export const ui = {
  en: {
    navLabel: 'Sections',
    homeLabel: 'Custodia, home',
    homeCrumb: 'Home',
    skip: 'Skip to content',
    cta: 'Live demo',
    navInventory: 'Inventory',
    navConnectors: 'Connectors',
    navInstall: 'Install',
    navLimits: 'Limits',
    otherCode: 'ES',
    otherLang: 'es',
    otherLabel: 'Versión en español',
    copy: 'Copy',
    copied: 'Copied',
    footInventory: 'Inventory',
    footConnectors: 'Connectors',
    footInstall: 'Install',
    footLimits: 'Limits',
    footDocs: 'Docs',
    footDemo: 'Live demo',
    footSource: 'Source on GitHub · Apache 2.0',
    footNote: 'custodia · prototype · 2026',
    docsHref: 'https://github.com/custodia-project/Custodia/tree/main/docs',
    reviewedLabel: 'Reviewed',
    statusLabel: 'Status',
    breadcrumbLabel: 'Breadcrumb',
    docsTitle: 'Documentation',
    docsNote: 'The detailed contracts live in the repository and are written in Spanish.',
    relatedTitle: 'Keep reading',
    entityDescription:
      'Custodia is an open-source, self-hosted inventory of security tools with declared metrics, provenance and completeness.',
    features: [
      'Read-only connector contracts for CyberArk PAM and Wazuh',
      'Metrics with source, data date and run completeness',
      'Self-hosted on Linux with Go and SQLite',
      'Local login with optional OIDC',
      'Signed install, update and rollback from the console',
    ],
  },
  es: {
    navLabel: 'Secciones',
    homeLabel: 'Custodia, inicio',
    homeCrumb: 'Inicio',
    skip: 'Saltar al contenido',
    cta: 'Demo en vivo',
    navInventory: 'Inventario',
    navConnectors: 'Conectores',
    navInstall: 'Instalar',
    navLimits: 'Límites',
    otherCode: 'EN',
    otherLang: 'en',
    otherLabel: 'English version',
    copy: 'Copiar',
    copied: 'Copiado',
    footInventory: 'Inventario',
    footConnectors: 'Conectores',
    footInstall: 'Instalar',
    footLimits: 'Límites',
    footDocs: 'Documentación',
    footDemo: 'Demo en vivo',
    footSource: 'Código en GitHub · Apache 2.0',
    footNote: 'custodia · prototipo · 2026',
    docsHref: 'https://github.com/custodia-project/Custodia/tree/main/docs',
    reviewedLabel: 'Revisado',
    statusLabel: 'Estado',
    breadcrumbLabel: 'Ruta',
    docsTitle: 'Documentación',
    docsNote: 'Los contratos detallados están en el repositorio, en español.',
    relatedTitle: 'Seguir leyendo',
    entityDescription:
      'Custodia es un inventario autohospedado y de código abierto de herramientas de seguridad, con métricas declaradas, procedencia y completitud.',
    features: [
      'Contratos de conector de solo lectura para CyberArk PAM y Wazuh',
      'Métricas con fuente, fecha de los datos y completitud de la corrida',
      'Autohospedado en Linux con Go y SQLite',
      'Login local con OIDC opcional',
      'Instalación, actualización y rollback firmados desde la consola',
    ],
  },
};

export const articles = { en: articlesEn, es: articlesEs };

const order = ['inventory', 'connectors', 'cyberark-pam', 'wazuh', 'install', 'limits'];
const keys = Object.keys(articlesEn);
if (keys.length !== order.length || order.some((key) => !articlesEn[key] || !articlesEs[key])) {
  throw new Error('articles-en.mjs y articles-es.mjs deben declarar las mismas páginas, en el orden acordado');
}
export const articleKeys = order;

export function fileFor(path) {
  if (path === '/') return 'index.html';
  return `${path.replace(/^\//, '')}index.html`;
}

export const allPages = [
  { key: 'home', lang: 'en', path: '/', file: 'index.html', isHome: true },
  { key: 'home', lang: 'es', path: '/es/', file: 'es/index.html', isHome: true },
  ...articleKeys.flatMap((key) => ['en', 'es'].map((lang) => {
    const page = articles[lang][key];
    return { key, lang, path: page.path, file: fileFor(page.path), isHome: false };
  })),
];

export const pageFiles = allPages.map((page) => page.file);

export function siblingOf(page) {
  const siblingLang = page.lang === 'en' ? 'es' : 'en';
  if (page.isHome) {
    return { lang: siblingLang, path: siblingLang === 'en' ? '/' : '/es/' };
  }
  return { lang: siblingLang, path: articles[siblingLang][page.key].path };
}
