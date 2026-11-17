// Gate for the product site's machine-readable layer.
//
// The regression this prevents is specific: when robots.txt, sitemap.xml or
// 404.html are missing, Cloudflare Pages falls back to index.html and answers
// 200 text/html for all of them. Build first, then check the files that the
// deploy would actually upload:
//
//   npm run build:site
//   node site/scripts/check-site.mjs
//
// After publishing, the same checks can run against the live host:
//
//   node site/scripts/check-site.mjs --live
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { extname, join, resolve } from 'node:path';
import { allPages, articles, base, reviewed, ui } from '../site/content.mjs';

const root = resolve(import.meta.dirname, '..');
const dist = join(root, 'dist');
const live = process.argv.includes('--live');
const pageByPath = new Map(allPages.map((page) => [page.path, page]));
const groups = new Map();
for (const page of allPages) {
  const group = groups.get(page.key) ?? [];
  group.push(page);
  groups.set(page.key, group);
}
const alternatesByKey = new Map();
for (const [key, group] of groups) {
  const en = group.find((page) => page.lang === 'en');
  const es = group.find((page) => page.lang === 'es');
  assert.ok(en && es, `${key} has no en/es pair`);
  alternatesByKey.set(key, { en: `${base}${en.path}`, es: `${base}${es.path}`, 'x-default': `${base}${en.path}` });
}

const read = async (name) => {
  try {
    return await readFile(join(dist, name), 'utf8');
  } catch {
    assert.fail(`${name} is missing from site/dist; run "npm run build:site" first`);
  }
};
const looksLikeHtml = (text) => /<(!doctype\s+html|html(?:\s|>))/i.test(text);
const attr = (html, pattern, label) => {
  const match = pattern.exec(html);
  assert.ok(match, `${label} is missing`);
  return match[1];
};

const robots = await read('robots.txt');
assert.ok(!looksLikeHtml(robots), 'robots.txt is HTML, not text');
assert.match(robots, /^User-agent: \*/m);
assert.match(robots, /^Allow: \/$/m);
assert.ok(robots.includes('User-agent: OAI-SearchBot'), 'robots.txt must name OAI-SearchBot');
assert.ok(robots.includes('User-agent: GPTBot'), 'robots.txt must keep training crawlers separate');
assert.ok(robots.includes(`Sitemap: ${base}/sitemap.xml`), 'robots.txt must point at the canonical sitemap');

const sitemap = await read('sitemap.xml');
assert.ok(!looksLikeHtml(sitemap), 'sitemap.xml is HTML, not XML');
assert.match(sitemap, /^<\?xml version="1\.0" encoding="UTF-8"\?>/);
assert.match(sitemap, /<urlset xmlns="http:\/\/www\.sitemaps\.org\/schemas\/sitemap\/0\.9"/);
const blocks = [...sitemap.matchAll(/<url>([\s\S]*?)<\/url>/g)].map((match) => match[1]);
assert.equal(blocks.length, allPages.length, 'sitemap must list every page once');
const seen = new Set();
for (const block of blocks) {
  const location = attr(block, /<loc>([^<]+)<\/loc>/, 'sitemap <loc>');
  const page = pageByPath.get(location.slice(base.length));
  assert.ok(page, `sitemap URL has no page: ${location}`);
  assert.ok(!seen.has(location), `sitemap repeats ${location}`);
  seen.add(location);
  const gotAlternates = Object.fromEntries(
    [...block.matchAll(/<xhtml:link rel="alternate" hreflang="([^"]+)" href="([^"]+)"/g)]
      .map((match) => [match[1], match[2]]),
  );
  assert.deepEqual(gotAlternates, alternatesByKey.get(page.key), `sitemap alternates differ for ${location}`);
}
assert.equal(seen.size, allPages.length, 'sitemap and page registry differ');

const notFound = await read('404.html');
assert.match(notFound, /<meta name="robots" content="noindex"\s*\/?>/, '404.html must be noindex');
assert.ok(!notFound.includes('rel="canonical"'), '404.html must not declare a canonical URL');
assert.ok(notFound.includes('href="/"'), '404.html must link to the home page');
assert.ok(notFound.includes('href="/es/"'), '404.html must link to the Spanish site');
assert.ok(notFound.includes('lang="es"'), '404.html must include the Spanish explanation');
assert.ok(!notFound.includes('{{'), '404.html kept a template placeholder');
assert.notEqual(notFound, await read('index.html'), '404.html is the home page, not an error page');

for (const page of allPages) {
  const html = await read(page.file);
  assert.ok(!html.includes('{{'), `${page.file} kept a template placeholder`);
  assert.equal(
    attr(html, /<html lang="([^"]+)"/, `${page.file} <html lang>`),
    page.lang,
    `${page.file} language differs`,
  );
  const canonical = `${base}${page.path}`;
  assert.equal(
    attr(html, /<link rel="canonical" href="([^"]+)"/, `${page.file} canonical`),
    canonical,
    `${page.file} canonical differs`,
  );
  const gotAlternates = Object.fromEntries(
    [...html.matchAll(/<link rel="alternate" hreflang="([^"]+)" href="([^"]+)"/g)]
      .map((match) => [match[1], match[2]]),
  );
  assert.deepEqual(gotAlternates, alternatesByKey.get(page.key), `${page.file} hreflang alternates differ`);
  const title = attr(html, /<title>([^<]+)<\/title>/, `${page.file} title`);
  const description = attr(html, /<meta name="description" content="([^"]+)"/, `${page.file} description`);
  assert.ok(title.length > 10, `${page.file} title is too short`);
  assert.ok(description.length > 40, `${page.file} description is too short`);
  assert.equal((html.match(/<h1\b/g) ?? []).length, 1, `${page.file} must have one h1`);
  assert.match(
    html,
    /<meta name="robots" content="index,follow,max-snippet:-1,max-image-preview:large"\s*\/?>/,
    `${page.file} must allow full snippets`,
  );

  const scripts = [...html.matchAll(/<script type="application\/ld\+json">([\s\S]*?)<\/script>/g)];
  assert.equal(scripts.length, 1, `${page.file} must have one JSON-LD block`);
  const data = JSON.parse(scripts[0][1]);
  assert.equal(data['@context'], 'https://schema.org');
  const graph = data['@graph'];
  assert.ok(Array.isArray(graph), `${page.file} JSON-LD has no @graph`);
  const website = graph.find((item) => item['@type'] === 'WebSite');
  const software = graph.find((item) => item['@type'] === 'SoftwareApplication');
  assert.ok(website, `${page.file} JSON-LD has no WebSite`);
  assert.ok(software, `${page.file} JSON-LD has no SoftwareApplication`);
  assert.equal(website.url, `${base}/`, `${page.file} WebSite URL differs`);
  assert.deepEqual(website.inLanguage, ['en', 'es'], `${page.file} WebSite languages differ`);
  assert.equal(website.description, ui[page.lang].entityDescription, `${page.file} WebSite description differs`);
  assert.equal(software.name, 'Custodia');
  assert.equal(software.url, `${base}/`, `${page.file} SoftwareApplication URL differs`);
  assert.equal(software.description, ui[page.lang].entityDescription, `${page.file} SoftwareApplication description differs`);
  assert.equal(software.applicationCategory, 'SecurityApplication');
  assert.match(software.operatingSystem, /Linux/);
  assert.equal(software.isAccessibleForFree, true);
  assert.equal(software.license, 'https://www.apache.org/licenses/LICENSE-2.0');
  assert.ok(Array.isArray(software.featureList) && software.featureList.length >= 3, `${page.file} featureList is incomplete`);
  assert.equal(software.softwareVersion, undefined, `${page.file} must not claim an unreleased version`);
  assert.equal(software.offers, undefined, `${page.file} must not claim an offer`);

  const article = graph.find((item) => item['@type'] === 'TechArticle');
  const crumbs = graph.find((item) => item['@type'] === 'BreadcrumbList');
  if (page.isHome) {
    assert.equal(article, undefined, `${page.file} must not describe the home page as an article`);
    assert.equal(crumbs, undefined, `${page.file} must not emit breadcrumbs for the home page`);
  } else {
    const expected = articles[page.lang][page.key];
    assert.ok(article, `${page.file} JSON-LD has no TechArticle`);
    assert.equal(article.headline, expected.headline, `${page.file} TechArticle headline differs`);
    assert.equal(article.description, description, `${page.file} TechArticle description differs from the page`);
    assert.equal(article.inLanguage, page.lang);
    assert.equal(article.dateModified, reviewed);
    assert.equal(article.mainEntityOfPage, canonical);
    assert.ok(crumbs, `${page.file} JSON-LD has no BreadcrumbList`);
    assert.ok(Array.isArray(crumbs.itemListElement) && crumbs.itemListElement.length >= 2, `${page.file} breadcrumbs are incomplete`);
    const last = crumbs.itemListElement.at(-1);
    assert.equal(last.item, canonical, `${page.file} breadcrumb does not end on the page`);
    assert.equal(last.position, crumbs.itemListElement.length);
  }
}

// Cloudflare Pages answers a missing path with the root 404.html and a 404
// status. Reproduce that contract locally so a missing file cannot pass the
// gate and become a 200 HTML fallback on the next deploy.
const types = {
  '.css': 'text/css; charset=utf-8',
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.png': 'image/png',
  '.svg': 'image/svg+xml',
  '.txt': 'text/plain; charset=utf-8',
  '.webp': 'image/webp',
  '.woff2': 'font/woff2',
  '.xml': 'application/xml; charset=utf-8',
};
const server = createServer(async (request, response) => {
  const pathname = decodeURIComponent(new URL(request.url, base).pathname);
  const relative = pathname === '/'
    ? 'index.html'
    : pathname.endsWith('/')
      ? `${pathname.slice(1)}index.html`
      : pathname.slice(1);
  try {
    const body = await readFile(join(dist, relative));
    response.writeHead(200, { 'content-type': types[extname(relative)] ?? 'application/octet-stream' });
    response.end(body);
  } catch {
    try {
      const body = await readFile(join(dist, '404.html'));
      response.writeHead(404, { 'content-type': 'text/html; charset=utf-8' });
      response.end(body);
    } catch {
      response.writeHead(500, { 'content-type': 'text/plain; charset=utf-8' });
      response.end('404.html is missing');
    }
  }
});
await new Promise((resolvePromise, reject) => {
  server.once('error', reject);
  server.listen(0, '127.0.0.1', resolvePromise);
});
const address = server.address();
assert.ok(address && typeof address === 'object', 'local server did not open a TCP port');
const local = async (path) => {
  const response = await fetch(`http://127.0.0.1:${address.port}${path}`);
  return { response, body: await response.text() };
};
try {
  for (const [path, type] of [['/robots.txt', /^text\/plain/], ['/sitemap.xml', /^application\/xml/]]) {
    const { response, body } = await local(path);
    assert.equal(response.status, 200, `local ${path} answered ${response.status}`);
    assert.match(response.headers.get('content-type') ?? '', type, `local ${path} has the wrong content type`);
    assert.ok(!looksLikeHtml(body), `local ${path} is HTML`);
  }
  for (const page of allPages) {
    const { response, body } = await local(page.path);
    assert.equal(response.status, 200, `local ${page.path} answered ${response.status}`);
    assert.match(response.headers.get('content-type') ?? '', /^text\/html/, `local ${page.path} is not HTML`);
    assert.ok(body.includes('application/ld+json'), `local ${page.path} has no JSON-LD`);
  }
  const missing = await local(`/__custodia_not_found_${Date.now()}`);
  assert.equal(missing.response.status, 404, 'local unknown path did not answer 404');
  assert.match(missing.response.headers.get('content-type') ?? '', /^text\/html/, 'local 404 is not HTML');
  assert.match(missing.body, /Page not found/, 'local 404 is not the custom page');
} finally {
  server.closeIdleConnections();
  await new Promise((resolvePromise) => server.close(resolvePromise));
}

if (live) {
  const request = async (path) => {
    const response = await fetch(`${base}${path}`, {
      redirect: 'manual',
      headers: { 'user-agent': 'custodia-site-check/1.0' },
    });
    return { response, body: await response.text() };
  };
  for (const [path, type] of [['/robots.txt', /^text\/plain/], ['/sitemap.xml', /xml/]]) {
    const { response, body } = await request(path);
    assert.equal(response.status, 200, `live ${path} answered ${response.status}`);
    assert.match(response.headers.get('content-type') ?? '', type, `live ${path} has the wrong content type`);
    assert.ok(!looksLikeHtml(body), `live ${path} is HTML`);
  }
  for (const page of allPages) {
    const { response, body } = await request(page.path);
    assert.equal(response.status, 200, `live ${page.path} answered ${response.status}`);
    assert.match(response.headers.get('content-type') ?? '', /^text\/html/, `live ${page.path} is not HTML`);
    assert.ok(body.includes('application/ld+json'), `live ${page.path} has no JSON-LD`);
  }

  const missingPath = `/__custodia_not_found_${Date.now()}`;
  const missing = await request(missingPath);
  assert.equal(missing.response.status, 404, `live ${missingPath} answered ${missing.response.status}, not 404`);
  assert.match(missing.response.headers.get('content-type') ?? '', /^text\/html/, 'live 404 is not HTML');
  assert.match(missing.body, /Page not found/, 'live 404 is not the custom page');
}

process.stdout.write(
  `Sitio OK: robots, sitemap (${allPages.length} URLs), 404 y JSON-LD de ${allPages.length} páginas; HTTP local 200/404.\n`,
);
if (live) process.stdout.write('Descubrimiento en vivo OK: tipos, 404 y JSON-LD.\n');
