// Submit the live sitemap to IndexNow after a deploy. The key file is public
// by design: IndexNow checks it at https://<host>/<key>.txt. A 200 or 202 means
// the URLs were accepted; it does not guarantee indexing.
//
//   npm run indexnow
//   npm run indexnow -- --dry-run
import { readFile, readdir } from 'node:fs/promises';
import { join, resolve } from 'node:path';

const base = 'https://custodia.octaviosantacruz.com';
const host = new URL(base).host;
const endpoint = process.env.INDEXNOW_ENDPOINT || 'https://api.indexnow.org/indexnow';
const dryRun = process.argv.includes('--dry-run');
const publicDir = resolve(import.meta.dirname, '..', 'public');

const keys = (await readdir(publicDir))
  .filter((name) => /^[a-f0-9]{32}\.txt$/.test(name));
if (keys.length !== 1) {
  throw new Error(`expected one 32-hex IndexNow key in site/public, found ${keys.length}`);
}
const key = keys[0].replace(/\.txt$/, '');
const keyBody = (await readFile(join(publicDir, keys[0]), 'utf8')).trim();
if (keyBody !== key) throw new Error(`${keys[0]} does not contain its own key`);

const sitemapResponse = await fetch(`${base}/sitemap.xml`, { redirect: 'manual' });
if (sitemapResponse.status !== 200) throw new Error(`live sitemap answered ${sitemapResponse.status}`);
const sitemap = await sitemapResponse.text();
const urls = [...sitemap.matchAll(/<loc>([^<]+)<\/loc>/g)].map((match) => match[1]);
if (!urls.length) throw new Error('live sitemap has no URLs');
for (const url of urls) {
  if (!url.startsWith(`${base}/`)) throw new Error(`sitemap URL leaves the site: ${url}`);
}

const keyResponse = await fetch(`${base}/${key}.txt`, { redirect: 'manual' });
if (keyResponse.status !== 200) throw new Error(`live key file answered ${keyResponse.status}`);
if (!/^text\/plain\b/i.test(keyResponse.headers.get('content-type') ?? '')) {
  throw new Error('live key file is not text/plain');
}
if ((await keyResponse.text()).trim() !== key) throw new Error('live key file does not contain the key');

if (dryRun) {
  process.stdout.write(`IndexNow dry run: ${urls.length} URLs, key ${key}.\n`);
  process.exit(0);
}

const response = await fetch(endpoint, {
  method: 'POST',
  headers: { 'content-type': 'application/json; charset=utf-8' },
  body: JSON.stringify({
    host,
    key,
    keyLocation: `${base}/${key}.txt`,
    urlList: urls,
  }),
});
const body = (await response.text()).trim();
if (response.status !== 200 && response.status !== 202) {
  throw new Error(`IndexNow answered ${response.status}${body ? `: ${body}` : ''}`);
}
process.stdout.write(`IndexNow accepted ${urls.length} URLs (status ${response.status}).\n`);
