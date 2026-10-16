# SPAR + Super Kit-Go automatic crawler adapters

The Go application now contains two **website-based** crawler adapters, scheduled by the existing cron and persisted in the existing SQLite database. There are **no manually maintained CSV imports** for either chain.

## Sources and actual coverage

| Chain | Official public starting point | Adapter | Verified in this project |
| --- | --- | --- | --- |
| SPAR North Macedonia | https://spar.mk/pricelist | `spar_directory` → `spar_store` | Official store links and total count visible via web search; parser, page streaming and database updates verified using local fixtures. **Live JavaScript network/pagination has not been tested.** |
| Super Kit-Go | https://www.superkitgo.mk/cenovnik.html | `kitgo_directory` → `kitgo_store` | Official price-list directory visible via web search; HTML/CSV formats and in-origin link discovery covered by local tests. **Linked file format, JavaScript behavior, and full-catalog coverage unverified.** |

The stores shown by SPAR and Kit-Go have many **identical numerical IDs and locations**. They may reflect brand transitions, related businesses or duplicate listings. Do not treat them as independently visitable supermarkets or add both to split-basket shopping routes without checking each physical store.

SPAR store price pages visibly print a total like `Маркет 1 ... Производи`, but initially render the table as `Се вчитуваат производите...` (loading). Ordinary HTTP parsing therefore cannot verify a complete product feed. A local, optional Playwright bridge renders the JavaScript page and advances through displayed pages. Go verifies there are no repeated pages, the stream ends successfully, and the **number of priced rows equals the retailer's advertised total**. If that check cannot pass, the crawl is marked `error` and previous products are preserved.

Kit-Go's store directory lists branches and price-list links. The adapter follows only actual links discovered on the retailer website (never invents URLs based on sequential numbers). If a linked list is a directly downloadable **CSV** with the supported `name` + `price_mkd` format, it is downloaded automatically from the website and ingested as a crawler feed (not a manual import). HTML price lists are accepted only with a count that proves completeness. Browser-rendered tables are supported when they expose a total count. Other downloaded formats such as PDF/XLSX are currently logged as unsupported, rather than guessed from text. This means the new Kit-Go adapter may return no products until its linked format is verified or a format-specific parser is supplied.

**Neither crawler has been tested against the live website from inside the Go/Docker runtime. Do not assume successful full-catalog ingestion until `/v1/sync-runs` confirms it.**

## Docker Desktop for Windows

1. Install Docker Desktop, enable Linux containers and WSL 2.
2. Extract the ZIP and open PowerShell inside `grocery-api`.
3. Start live mode (includes Go + internal headless browser):

```powershell
docker compose up --build -d
Invoke-RestMethod 'http://localhost:8080/healthz'
Invoke-RestMethod 'http://localhost:8080/v1/sync-runs'
Invoke-RestMethod 'http://localhost:8080/v1/products?market=spar&limit=20'
Invoke-RestMethod 'http://localhost:8080/v1/products?market=kitgo&limit=20'
```

Swagger: http://localhost:8080/docs . The browser bridge has **no published host port**, and only permits requests for the listed retailer origins. It can require significant memory/disk space because the Playwright image contains Chromium; allow at least 4GB Docker Desktop memory when running large crawls.

The existing `compose.demo.yaml` still runs the tiny synthetic demo on http://localhost:8081 without the browser container:

```powershell
docker compose -f compose.demo.yaml up --build -d
```

## Configure crawl size and schedule

Edit `sources.json`:

```json
{
  "id": "spar-store-directory",
  "market": "spar",
  "name": "SPAR price lists",
  "type": "spar_directory",
  "url": "https://spar.mk/pricelist",
  "max_stores": 2
}
```

And `kitgo-store-directory` with the `kitgo_directory` type and its official URL. `max_stores=2` is a **deliberate safe starting limit**: it marks the directory sync `partial`. Set `max_stores` to `0` to attempt all linked stores **after** validating the source, crawl volume, robots.txt, permissions and resource usage. Source `max_pages=5000` supports the large catalog sizes published by SPAR if the retailer provides small pages; the browser tries to switch to 100 rows per page where supported. A 70k-product store can still take a long time. Keep the 1.5-second delay or increase it, and consider caching identical market inventories when you can prove they are identical.

When you edit `sources.json` rebuild: `docker compose up --build -d`.

Keep `BROWSER_BRIDGE_URL=http://browser:3333` only within Compose's private network. Browser pages are source data, not trusted instructions. The Go fetcher respects the retailer's robots.txt and will refuse collection if robots.txt cannot be retrieved (other than a genuine HTTP 404). A web page returning only headers, a CAPTCHA or JavaScript placeholders never becomes a successful refresh.

## API changes

No new endpoints are necessary: existing API filters work automatically after successful ingestion:

```text
GET /v1/products?market=spar
GET /v1/products?market=kitgo
GET /v1/products?category=milk
GET /v1/products?market=spar&category=milk
GET /v1/stores
GET /v1/sync-runs
POST /v1/basket/quote
```

The OpenAPI 3.0.3 contract is unchanged. The same basket/price-comparison logic applies. Do not mix prices from stale or failed syncs without using their source dates/status, and do not confuse two possible aliases of the same SPAR/Kit-Go physical branch with two independent shopping stops.

## Validated offline

- Go build, tests, race detector and vet.
- SPAR and Kit-Go store discovery from in-origin links; external/non-store links rejected.
- Per-column price parsing, availability and authoritative counts.
- Browser NDJSON success/repeat/truncation/missing-final-event checks.
- Store-cap partial status and rollback on a failed refresh.
- Browser JS syntax and Compose YAML structure.

**Still needs a real Docker Desktop / live retailer smoke test:** verify Playwright can follow the actual pagination controls and that Kit-Go exposes parseable complete price lists (HTML or linked CSV). Docker was unavailable in the authoring environment, and the official websites were not reachable from its code runtime.
