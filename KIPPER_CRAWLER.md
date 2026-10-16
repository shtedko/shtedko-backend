# Kipper crawler — physical branches and online listings

## What was added

The live configuration (`sources.json`) now enables two **`kipper_directory`** discovery sources: Kipper's [market archive](https://kipper.mk/mk/category/market/) and its newer [market directory](https://kipper.mk/mk/marketet/), alongside the pre-existing **`kipper-online`** online catalog feed. Neither one uses a CSV import.

- `kipper_directory` discovers branch URLs with numbered Kipper store names, follows the numbered archive pages when available, and syncs discovered physical branches using `kipper_store`.
- `kipper_store` reads the retail-price table (item name, selling price, description, availability, regular price, source date) from an individual Kipper branch page. It never equates unit prices with checkout prices.
- `woo_shop` keeps collecting *explicitly priced* online shop entries separately under market `kipper-online`. These are **not** physical branch prices and cannot be used to assert in-store availability.

The two directories can overlap: the same numbered branch may be crawled twice per scheduled run. Their store IDs are deterministic, so it updates one store snapshot rather than creating duplicates. Both pages are **not proven to contain all branches**. It may list a small subset. Directory runs are marked `partial` even if all discovered branch pages succeed. Users can add more confirmed `kipper_store` URLs to `sources.json`. Do not infer or invent branch IDs.

## Important live-site limitation

A current public branch page may expose the **table heading but no product rows** in basic HTML (potentially JavaScript-loaded). The Go adapter cannot automatically retrieve those missing rows. In that situation it logs a `sync_runs` **error** for the branch and preserves any previous successful product snapshot. It does **not** add fake prices or call a zero-product crawl successful. Live Kipper coverage was **not tested directly** from this build environment due to unavailable outbound DNS.

If the branch site is actually populated through a private or public AJAX JSON endpoint, that endpoint must first be identified and its response verified. A future adapter can then consume it if public access and site terms permit it. Do not treat `kipper-online` items as a workaround for missing physical-store inventory.

## Start on Windows PowerShell

```powershell
# Docker Desktop running Linux containers (WSL 2)
docker compose up --build -d
docker compose logs --tail=100 api

# Branch prices only (empty until one branch sync succeeds)
Invoke-RestMethod 'http://localhost:8080/v1/products?market=kipper&limit=50'

# Separate online listings (partial)
Invoke-RestMethod 'http://localhost:8080/v1/products?market=kipper-online&limit=50'

# Inspect failed or partial crawler runs
Invoke-RestMethod 'http://localhost:8080/v1/sync-runs'
```

To crawl a specifically confirmed Kipper branch in addition to automatic discovery, add this `sources.json` entry to the `sources` array and rebuild:

```json
{
  "id": "kipper-041",
  "market": "kipper",
  "name": "Kipper 041, Kumanovo",
  "type": "kipper_store",
  "url": "https://kipper.mk/mk/kipper-41/"
}
```

If the directory also discovers that branch, it uses the same store ID and product snapshot; a duplicate crawl may occur. Remove the explicit source when automatic discovery is verified. The server keeps the scheduled 03:00 Europe/Skopje sync, user accounts, basket API, SQLite storage and OpenAPI unchanged.

## Development tests

`go test ./...` includes Kipper fixtures covering table parsing, empty/invalid price rejection, numbered branch discovery, multi-page archive discovery, transactional snapshot persistence, and fail-safe update behavior. **These tests simulate website markup; they are not evidence that live Kipper prices are currently extractable.**
