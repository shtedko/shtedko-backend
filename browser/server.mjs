// Internal-only browser bridge for dynamic SPAR / Kit-Go price grids.
// The Go API verifies robots.txt, count, pagination and snapshot integrity.
import { createServer } from 'node:http';
import { chromium } from 'playwright';

const port = Number(process.env.PORT || 3333);
const allow = new Set(['spar.mk', 'www.spar.mk', 'superkitgo.mk', 'www.superkitgo.mk']);
const pageTimeoutMs = Number(process.env.PAGE_TIMEOUT_MS || 45000);
const maxIncomingBytes = 4096;
let browser;

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
function validUrl(input) {
  const url = new URL(input);
  if (url.protocol !== 'https:' || !allow.has(url.hostname)) throw new Error('unsupported retailer host');
  if (url.username || url.password || url.port) throw new Error('invalid URL credentials/port');
  return url.href;
}
async function readRequest(req) {
  let body = '';
  for await (const fragment of req) {
    body += fragment;
    if (body.length > maxIncomingBytes) throw new Error('request too large');
  }
  return JSON.parse(body);
}
function send(res, code, body, contentType='text/plain; charset=utf-8') {
  if (!res.headersSent) res.writeHead(code, {'content-type': contentType, 'cache-control':'no-store'});
  res.end(body);
}
async function navigate(page, url) {
  await page.goto(url, {waitUntil:'domcontentloaded', timeout: pageTimeoutMs});
  await page.waitForTimeout(1800);
}
async function waitForData(page) {
  // SPAR currently paints the price-table header first, then loads rows by JS.
  await page.waitForFunction(() => {
    return [...document.querySelectorAll('table')].some(table => {
      const row = [...table.querySelectorAll('tbody tr')].find(tr =>
        tr.querySelectorAll('td').length > 1 &&
        !/се вчитуваат|loading|no data|нема податоци/i.test(tr.textContent || ''));
      return Boolean(row);
    });
  }, null, {timeout: pageTimeoutMs}).catch(() => {});
  return await page.content();
}
async function nextPage(page, currentFingerprint) {
  const selector = [
    '[data-testid="next-page"]', '[data-page="next"]',
    'button[aria-label*="Next" i]', 'a[aria-label*="Next" i]',
    'button[aria-label*="Следна" i]',
    '.paginate_button.next', '.pagination .next',
    '#products_next', '#productTable_next',
    'button:has-text("Следна")', 'a:has-text("Следна")',
    'button:has-text("Next")', 'a:has-text("Next")',
    'button:has-text("›")', 'a:has-text("›")',
    'button:has-text("»")', 'a:has-text("»")'
  ];
  for (const sel of selector) {
    let elements;
    try { elements = page.locator(sel); } catch { continue; }
    for (let i = 0; i < Math.min(await elements.count(),4); i++) {
      const element = elements.nth(i);
      if (!(await element.isVisible().catch(() => false))) continue;
      const disabled = await element.evaluate(e =>
        e.disabled || e.getAttribute('aria-disabled')==='true' ||
        e.classList.contains('disabled') || Boolean(e.closest('.disabled'))
      ).catch(() => true);
      if (disabled) continue;
      try {
        await element.click({timeout:8000});
        await page.waitForFunction(old => {
          const t = [...document.querySelectorAll('table')]
            .map(t => t.querySelector('tbody')?.innerText || '').join('|');
          return t && t !== old;
        }, currentFingerprint, {timeout:pageTimeoutMs});
        return true;
      } catch { return false; }
    }
  }
  return false;
}
async function withPage(url, handler) {
  if (!browser) browser = await chromium.launch({headless:true, args:['--no-sandbox']});
  const ctx = await browser.newContext({userAgent:'GroceryCatalogBot/0.1 (+respect robots.txt)',locale:'mk-MK'});
  try {
    const page = await ctx.newPage();
    await navigate(page,url);
    return await handler(page);
  } finally {
    await ctx.close();
  }
}
createServer(async (req,res) => {
  if (req.method === 'GET' && req.url === '/healthz') { send(res,200,'ok'); return; }
  if (req.method !== 'POST' || !['/render','/crawl'].includes(req.url)) {
    send(res,404,'not found'); return;
  }
  try {
    const args = await readRequest(req);
    const url = validUrl(args.url);
    if (req.url === '/render') {
      const html = await withPage(url, async page => {
        await page.waitForTimeout(2200);
        return page.content();
      });
      if (html.length > 12*1024*1024) throw new Error('rendered directory too large');
      send(res,200,html,'text/html; charset=utf-8');
      return;
    }
    const maxPages = Math.min(8000, Math.max(1, Number(args.max_pages || 1500)));
    const delayMs = Math.min(10000, Math.max(600, Number(args.delay_ms || 1500)));
    res.writeHead(200, {'content-type':'application/x-ndjson; charset=utf-8','cache-control':'no-store'});
    // Keep streaming, so Go can stop the crawl without loading full catalogs
    // into the sidecar's memory.
    await withPage(url, async page => {
      // Prefer 100 products per page where the retailer exposes that option.
      // Do not change store, category or filter dropdowns.
      for (const selector of ['.dataTables_length select','select[name*="length"]','select[aria-label*="page size" i]']) {
        const select=page.locator(selector).first();
        if (await select.count() && await select.locator('option[value="100"]').count()) {
          await select.selectOption("100").catch(() => {});
          await page.waitForTimeout(900);
          break;
        }
      }
      const seen = new Set();
      for (let n = 1; n <= maxPages; n++) {
        const html = await waitForData(page);
        const fp = await page.evaluate(() => [...document.querySelectorAll('table')]
          .map(t => t.querySelector('tbody')?.innerText || '').join('|'));
        if (!fp || seen.has(fp)) throw new Error(`empty/repeating page ${n}`);
        seen.add(fp);
        res.write(JSON.stringify({html})+'\n');
        if (res.destroyed) throw new Error('client disconnected');
        await sleep(delayMs);
        if (!await nextPage(page,fp)) {
          res.write(JSON.stringify({complete:true})+'\n');
          return;
        }
      }
      throw new Error(`exceeded max_pages=${maxPages}`);
    });
    res.end();
  } catch (error) {
    const message = String(error?.message || error).slice(0,500);
    if (res.headersSent) {res.write(JSON.stringify({error:message})+'\n');res.end();}
    else send(res,502,message);
  }
}).listen(port,'0.0.0.0',() => console.log(`Browser bridge listening on ${port}`));
process.on('SIGTERM',async ()=>{await browser?.close();process.exit(0)});
