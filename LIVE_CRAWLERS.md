# Automatic KAM, Stokomak, and Žito crawling

Updated October 16, 2026. This project is a Go + SQLite API that runs through Docker Compose on Windows (Docker Desktop with Linux containers and WSL2).

## Coverage by retailer

| Retailer | Go adapter | Source | Completeness |
|---|---|---|---|
| Stokomak | `price_portal_directory` + `price_portal` | https://stokomak.proverkanaceni.mk/ | **Complete for each successfully crawled store price list**, based on published row count; not all stores unless discovered/configured |
| Žito | `price_portal_directory` + `price_portal` | https://zito.proverkanaceni.mk/ | **Complete for each successfully crawled store price list**, based on published row count; not all stores unless discovered/configured |
| KAM | `kam_offers` | https://www.kam.com.mk/ | **Partial priced promotions only**. Its full current inventory could not be verified as an accessible static HTML price list |

**None of these three require CSV files.** The production `sources.json` uses automatic HTTPS website crawlers for all three. The bundled `sources.demo.json` still uses fictitious CSV sample data, intentionally, so the demo works offline.

For Stokomak, `org=5` is an observed public store ID; for Žito, `org=13`. The crawler reads the price-list site's public store dropdown to discover more stores. If that menu is JavaScript-only or not readable, it collects the configured fallback store and records a `partial` directory status. Each store gets its own store ID; prices are not assumed to be the same across branches.

## How to run on Windows

1. Install Docker Desktop, enable WSL2 / Linux containers.
2. Extract the ZIP and open PowerShell in `grocery-api`.
3. Run:

```powershell
docker compose up --build -d
docker compose logs -f api
```

The public API and embedded OpenAPI/Swagger UI remain at `http://localhost:8080/docs`. Sync runs immediately at startup and again daily at 03:00 Europe/Skopje time.

Try these from PowerShell:

```powershell
Invoke-RestMethod http://localhost:8080/v1/sync-runs
Invoke-RestMethod 'http://localhost:8080/v1/products?market=stokomak&limit=50'
Invoke-RestMethod 'http://localhost:8080/v1/products?market=zito&limit=50'
Invoke-RestMethod 'http://localhost:8080/v1/products?market=kam&limit=50'
Invoke-RestMethod 'http://localhost:8080/v1/products?category=milk&limit=50'
```

For offline fake data and a quick mobile API demonstration:

```powershell
docker compose -f compose.demo.yaml up --build -d
```

Open `http://localhost:8081/docs` for the demo. **Demo prices are not real.**

## How correctness and failure handling work

- The HTML catalog parser extracts product names, prices, regular prices, availability, categories/descriptions, and the source's last-update date when present.
- The `price_portal` adapters fetch page 1, page 2, ... using `perPage=100` until they exactly match the source-reported catalog count. An inconsistent total, a repeated page, a blocked response, a malformed price table, or reaching the page limit produces an error.
- Changes are written to SQLite using an atomic database transaction only after the complete collection succeeds. A failed crawl never deletes the previous successful snapshot.
- Store discovery is distinct from catalog completeness. `partial` may mean only a fallback store could be reached; it does **not** mean every store was found.
- KAM promotions cannot guarantee in-store availability, and are marked `partial`; offers with missing prices are omitted. It would be misleading to use them as evidence of a complete KAM basket.
- The crawl uses robots.txt checks and configurable throttling (default 1.5s per request). Respect the retailer's website terms and check `/v1/sync-runs` regularly.

## Tests and limitations

Automated Go tests exercise portal pagination, changing totals, repeated pages, store menus, manual fallback behavior, price data parsing, failed refresh preservation, KAM HTML/JSON-LD offers, authentication, basket APIs, and existing crawler adapters. Both `go test ./...` and `go test -race ./...`, `go vet ./...`, and a compiled offline demo ingestion passed on Linux.

There is no outbound DNS access from the build container. The live KAM, Stokomak, and Žito endpoints **were researched online**, but their current HTML bytes could not be downloaded or tested in this runtime. Real-world selector changes, robots restrictions, and Docker Desktop behavior on your Windows machine still need checking. A missing/blocked source causes a visible sync error, not fabricated prices.

## Configure another confirmed branch

Add to the `sources` array in `sources.json`:

```json
{
  "id": "stokomak-confirmed-store-id",
  "market": "stokomak",
  "name": "Stokomak confirmed branch",
  "type": "price_portal",
  "url": "https://stokomak.proverkanaceni.mk/",
  "org": "YOUR_CONFIRMED_NUMERIC_STORE_ID"
}
```

Replace the placeholder with the real price-list `org` number. Rebuild with `docker compose up --build -d` when source settings change.

## Kipper branch collection

Kipper uses a separate `kipper_directory` crawler for store discovery and `kipper_store` for published store-level HTML tables. It is enabled by default alongside the `kipper-online` WooCommerce crawler. The archive does not prove complete store discovery, and some public branch pages render headers with no HTML price rows. Such pages explicitly fail rather than importing empty lists or using online shop prices as physical-store prices. See [KIPPER_CRAWLER.md](KIPPER_CRAWLER.md).
