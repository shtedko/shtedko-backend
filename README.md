# Grocery Catalog API (Go + SQLite)

A **runnable, dependency-light Go 1.23 service** for collecting public supermarket product listings, storing per-store records, and serving searchable REST results. It uses **no AI**, no third-party Go modules, and a durable SQLite database (via the system SQLite C library). The source adapters and tests are included.

> **Data completeness / status:** The HTTP parsers were built against the public price-list structures visible in October 2026, but **could not be tested against the live retailer websites from this execution environment** because the build environment has no outbound DNS/network access. Tinex and Ramstore use best-effort public HTML crawlers. KAM and Stokomak currently **require an externally supplied CSV or CSV feed**; their entire product catalogs cannot be retrieved using this package's default settings. The app does not ship with a prepopulated/live product database. `GET /v1/stores` and `/v1/sync-runs` surface collection errors. A source with an absent feed is intentionally shown as an error, not as a successful empty crawl.

## Mobile app API (new)

The implementation now includes **persistent private baskets, stateless basket quotes, best complete single-store totals, optimized split-store totals (optional max 1–3 store visits), offer comparisons normalized by whole-pack quantities, observed price changes, and market summaries**. See **[MOBILE_API.md](MOBILE_API.md)** for all endpoints, request/response formats, privacy requirements, and exact demo commands. A standalone demonstration imports `sources.demo.json` and four **synthetic** CSV feeds from `demo/`; its prices are **not live**.

Key endpoints added: `GET /v1/markets`, `GET /v1/comparisons`, `POST /v1/basket/quote`, `POST /v1/baskets`, `GET/DELETE /v1/baskets/{id}`, `POST /v1/baskets/{id}/items`, `PATCH/DELETE /v1/baskets/{id}/items/{item_id}`, `POST /v1/baskets/{id}/quote`, `GET /v1/products/{id}`, and `GET /v1/products/{id}/price-history`. Comparisons require explicit opt-in for **unverified** category equivalence; unknown or unavailable items are conservatively excluded. Totals use full packages and never silently count incomplete baskets as complete.

## Windows-ready Docker Compose (recommended)

**Windows 10/11 + Docker Desktop (WSL 2 / Linux containers):** open PowerShell in this project folder. There is no need to install Go or SQLite locally. See the step-by-step [WINDOWS_DOCKER.md](WINDOWS_DOCKER.md), including PowerShell smoke tests, ports, and troubleshooting.

```powershell
# Works without retailer connectivity: 4 synthetic demo feeds, port 8081
docker compose -f compose.demo.yaml up --build -d
Invoke-RestMethod 'http://localhost:8081/v1/products?category=milk'
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\windows-smoke.ps1

# Live-config mode, port 8080
docker compose up --build -d
Invoke-RestMethod 'http://localhost:8080/healthz'
Start-Process 'http://localhost:8080/docs'
```

The two Compose files are independent, use different ports (**8080/8081**) and **separate Docker named volumes**. Database files persist through normal `docker compose down` / `up` cycles. The API runs as a non-root user in a Debian-based Linux container with a system SQLite C library and timezone data. Cron scheduling defaults to **03:00 Europe/Skopje**.

Edit `.env` (copy `.env.example` first) for ports and optional admin credentials. Place real KAM/Stokomak CSV feeds under `imports/`. Edit `sources.json` to add stores and **rebuild** to pick up source configuration changes (`docker compose up --build -d`). The default source configuration is baked into the image to avoid host-file mounts that can behave inconsistently on Windows. The `imports` directory is the only bind mount, and the database stays in a named volume. Any absent CSV or failed crawl appears in `/v1/sync-runs` rather than being presented as successful catalog collection.

For hosts other than Windows, the same Compose commands work in a Bash terminal.

## Run without Docker

Requirements: **Go 1.23+**, C compiler and `libsqlite3-dev` (`libsqlite3` runtime). On Debian/Ubuntu:

```bash
sudo apt-get install gcc libsqlite3-dev
CGO_ENABLED=1 go run . -config sources.json -db data/groceries.sqlite
```

Run a single synchronous collection without launching the server:

```bash
CGO_ENABLED=1 go run . -once
# Or start the API without its immediate first collection:
CGO_ENABLED=1 go run . -no-startup
```

Run tests:

```bash
CGO_ENABLED=1 go test ./...
CGO_ENABLED=1 go vet ./...
```

## REST API

All responses are JSON, prices are in **MKD** (stored as integer denar cents, exposed as decimal MKD), and timestamps are preserved if the original source supplies them.

| Method | Endpoint | Purpose |
|---|---|---|
| GET | `/healthz` | Database health |
| GET | `/v1/stores` | Stores/markets, successful last sync, errors and active item counts |
| GET | `/v1/products` | Paginated products, including store and market |
| GET | `/v1/categories` | Categories with active product counts |
| GET | `/v1/comparison-groups` | **Unverified** similar product-specification buckets spanning markets |
| GET | `/v1/sync-runs` | Latest per-source job statuses and errors |
| POST | `/v1/admin/sync` | Protected manual sync (whole configured catalog or one source) |

Examples:

```bash
# Everything (pagination defaults to 50, max 200)
curl 'http://localhost:8080/v1/products?limit=100&offset=0'

# Milk from ALL connected markets
curl 'http://localhost:8080/v1/products?category=milk'

# Meat in one market / one individual store
curl 'http://localhost:8080/v1/products?category=meat&market=ramstore'
curl 'http://localhost:8080/v1/products?category=bread&store=ramstore-vodno-2'

# Search supports Macedonian Cyrillic/Latin word spellings
curl 'http://localhost:8080/v1/products?q=mleko&available=true'
curl 'http://localhost:8080/v1/products?q=%D0%BC%D0%BB%D0%B5%D0%BA%D0%BE'

# Only active products are returned by default; inspect historical records:
curl 'http://localhost:8080/v1/products?include_inactive=true&market=tinex'

# Compare milk specifications present in at least two markets (NOT identical SKUs)
curl 'http://localhost:8080/v1/comparison-groups?category=milk&min_markets=2'

# See failed imports / crawling coverage
curl 'http://localhost:8080/v1/sync-runs'

# Trigger all collectors, or an individual configured source (blocks until finished):
curl -X POST -H 'Authorization: Bearer replace-with-a-long-random-value' \
  'http://localhost:8080/v1/admin/sync'
curl -X POST -H 'Authorization: Bearer replace-with-a-long-random-value' \
  'http://localhost:8080/v1/admin/sync?store=tinex-512'
```

Product response shape:

```json
{
  "total": 1,
  "limit": 50,
  "offset": 0,
  "items": [
    {
      "id": "sha256-derived-id",
      "store_id": "ramstore-vodno-2",
      "store_name": "Ramstore Vodno 2",
      "market": "ramstore",
      "name": "EXAMPLE МЛЕКО 3.2% 1Л",
      "description": "ТРАЈНО МЛЕКО",
      "category": "milk",
      "comparison_key": "milk|unspecified|uht|3.2|plain|normal",
      "size_value": 1000,
      "size_unit": "ml",
      "price_mkd": 79,
      "currency": "MKD",
      "available": true,
      "active": true,
      "source_url": "https://ramstore.com.mk/marketi/ramstore-vodno-2/",
      "source_updated_at": "10.10.2026",
      "last_seen_at": "2026-10-11T12:00:00Z",
      "regular_price_mkd": null
    }
  ]
}
```

This JSON is an **illustrative API schema**, not proof that the example item is currently on sale at that price.

### Filtering parameters

`GET /v1/products` supports: `category` (`milk`, `bread`, `meat`, `eggs`, `yogurt`, `cheese`, `flour`, `rice`, `oil`, `other`), `market`, `store`, `q`, `available=true|false`, `include_inactive=true`, `limit` (1–200), and `offset` (0–1,000,000).

## Configure the retailer adapters

Edit `sources.json`. Each source must have a unique `id`, market code, name, `type`, and `url`. Multiple independent stores can share a market code. Supported adapter types:

* `tinex`: reads the Tinex public paginated price table for the **explicit configured `org` (store)**. Example uses the publicly observed `org=512`. Add additional org IDs as separate source records if you want additional stores. The crawler stops only after reaching the site's reported total and refuses repeats or max-page exhaustion; verify the published HTML selector and robots policy before relying on it.
* `ramstore_html`: reads **one published store-level price table**. The provided example uses Ramstore Vodno 2.
* `ramstore_directory`: attempts to discover store page URLs on Ramstore's `marketi/` directory and runs a `ramstore_html` import for each. If the directory supplies links only through JavaScript, it reports an error. The explicit Vodno 2 source still provides a fallback.
* `csv`: reads a **local CSV** from a path, e.g. `file://imports/kam.csv`. This is the default KAM and Stokomak path. Files must be generated from a legitimately obtained *complete* feed if full catalog coverage is desired.
* `csv_url`: downloads a **public CSV feed** at its configured HTTPS URL with the same rate limiting/robots rules, then imports it. It works for **any** new supermarket providing a CSV feed; don't point it at an HTML page or PDF.

Official entry points used to design the source adapters:

- Tinex: https://ceni.tinex.mk/
- Ramstore store list: https://ramstore.com.mk/marketi/
- Ramstore store-level example: https://ramstore.com.mk/marketi/ramstore-vodno-2/
- KAM price-list page: https://www.kam.com.mk/ceni-vo-marketi.nspx
- Stokomak price-checking page: https://stokomak.com.mk/proverka-na-ceni/

**The KAM and Stokomak websites are NOT directly being bulk scraped by this project.** Their price pages require further integration work or authorized exports. Do not represent imported promotions as the full inventory.

### CSV format (one file per store)

Required columns: `name,price_mkd` (or `name,price`). Optional: `description,regular_price_mkd,available,source_updated_at`. UTF-8 input; CSV headers are case-insensitive; availability can be `Да`, `Не`, `true`, `false`, `yes`, `no`. If the source-update date is unknown, the source date is left blank rather than stamped with the import date. The separate `last_seen_at` always records collection time.

```csv
name,price_mkd,description,available,source_updated_at
"EXAMPLE МЛЕКО 3.2% 1Л",79.00,"ТРАЈНО МЛЕКО",true,2026-10-11
"EXAMPLE БЕЛ ЛЕБ 500Г",45.00,"ЛЕБ",true,2026-10-11
```

The sample above is **synthetic**. Save as `imports/kam.csv` (or `imports/stokomak.csv`) only for testing; replace with actual retailer data before comparing prices. Import updates should be atomic: write to a temporary file and rename when complete. You can add more stores by adding CSV source objects with unique IDs.

## Classification and matching (no AI)

`classify.go` transliterates Cyrillic→Latin, normalizes unit suffixes and quantities, and applies category rules to names and descriptions. A deterministic optional `comparison_key` groups **possible comparable items** with matching known attributes (e.g., UHT processing, fat percentage, flavor, lactose content). Missing essential specifications yield an empty key. These are *candidates only*: no SKU is declared the same without EAN/barcode verification, and cross-brand/unit-size matches are not identical SKUs. Some product labels are incomplete, so manual review may still be needed.

`GET /v1/products?category=milk` lists all currently classified milk records irrespective of whether comparison keys exist.

## Data quality and operational safety

- **Never import a zero-product response** as a successful snapshot. Successful snapshots replace one store's active collection atomically; failures preserve previously collected products and log the error. A guard refuses snapshots with fewer than 60% of previously active products if the store previously held 25 or more items. Investigate legitimate large assortment reductions before overriding the guard.
- Store-level records keep their **own prices and availability**. Price changes update the same normalized per-store product ID; imported products without provider-specific SKU IDs can collide if two products in one store have identical normalized titles, so production should replace this heuristic with the retailer's stable item ID or GTIN where available.
- `source_updated_at` reflects a reported source date when present; `last_seen_at` is collection time. Neither proves an offer is still valid. The new `price_history` table records **observed changes** on successful snapshots; it does not model promotional validity windows or validate historic checkout prices.
- The crawlers throttle requests (default 1.5 seconds **between requests**), check `robots.txt`, stop on blocked/non-200 responses, and never attempt CAPTCHA bypass. Verify each site's terms and get permission before running large collection jobs. These sources can change their HTML structure at any time.
- The admin endpoint requires an `ADMIN_TOKEN` environment variable; otherwise it is disabled. Use HTTPS/reverse-proxy and a strong token before exposing this service beyond localhost. SQLite handles single-instance ingestion well; for multiple concurrent replicas or very large catalogs, migrate the repository layer to PostgreSQL.
- This project is a runnable **foundation**, not an audited production scraper or a verified complete nationwide product catalog. Source coverage should be monitored in `GET /v1/stores` and `GET /v1/sync-runs`.

## OpenAPI 3.0.3 contract & interactive docs

This repository ships a **complete checked-in OpenAPI contract** for every currently implemented v1 endpoint, including stateless basket quotes, saved baskets, category filters, cross-market comparisons, price history, ingestion status and protected manual sync.

With the server running:

- **Swagger UI:** `http://localhost:8080/docs` — interactive browser docs. The UI assets come from jsDelivr and require internet access; the JSON/YAML specs do not.
- **OpenAPI YAML:** `http://localhost:8080/openapi.yaml` — human-editable contract.
- **OpenAPI JSON:** `http://localhost:8080/openapi.json` — compatible with most code generators and SDK clients.

The YAML and JSON are embedded into the Go binary and work in Docker; no separate static web server is needed. **Contract tests** assert that every documented HTTP method/path resolves to an actual Go handler. See `OPENAPI_INTEGRATION.md` for Flutter, Kotlin, Swift and TypeScript SDK generation commands. Run `make openapi` after editing `scripts/generate_openapi.py` (requires Python 3 + PyYAML), then run `go test ./...` to check routes.

Security is intentionally operation-scoped in the spec: the catalog and stateless quote endpoints are public, saved basket endpoints use the `X-Basket-Token` secret obtained from `POST /v1/baskets`, and the sync endpoint uses `Authorization: Bearer <ADMIN_TOKEN>`. Product comparison groups are unverified candidates; the quote's nullable totals flag incomplete baskets. Do not interpret sample listings or prices as live or complete retailer inventory.
