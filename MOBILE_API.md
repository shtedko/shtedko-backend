# Mobile app API — price comparison and cheapest basket

This document describes the **implemented** mobile endpoints in this repository. All numbers in the worked example below come from the `demo/` CSV files and are **synthetic**, not market prices.

## Quickstart with deterministic demo data

From `grocery-api/` (requires Go 1.23, CGO, gcc, SQLite headers):

```bash
# Import sample feeds into a separate database
CGO_ENABLED=1 go run . -config sources.demo.json -db data/demo.sqlite -once
# Launch the API without a live collection attempt
CGO_ENABLED=1 go run . -config sources.demo.json -db data/demo.sqlite -no-startup
```

Find categories, nearby **configured** stores, and product records:

```bash
curl -s localhost:8080/v1/markets
curl -s localhost:8080/v1/stores
curl -s 'localhost:8080/v1/products?category=milk'
curl -s 'localhost:8080/v1/comparison-groups?category=milk'
```

A `comparison_key` is automatically calculated from selected product attributes **without AI**. It is an **unverified candidate group** and may contain non-equivalent items. To compare branded identical SKUs, add barcode / GTIN and manual equivalence verification in a future release.

## Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/v1/markets` | List configured markets with store/catalog counts |
| GET | `/v1/stores` | List physical store sources and sync health |
| GET | `/v1/categories` | Category facets |
| GET | `/v1/products?category=milk&market=tinex` | Filtered retail listings |
| GET | `/v1/products/{id}` | One retail listing with store metadata |
| GET | `/v1/products/{id}/price-history?limit=100` | Observed price changes, newest first |
| GET | `/v1/comparison-groups?category=milk` | Discover unverified comparable groups |
| GET | `/v1/comparisons?comparison_key=...&amount=2000&unit=ml` | All active offers ranked by *purchased checkout quantity*, across stores |
| POST | `/v1/basket/quote` | **Stateless quote**: best one-store basket and cheapest split basket |
| POST | `/v1/baskets` | Create private saved basket; returns basket ID and secret |
| GET | `/v1/baskets/{id}` | Read saved basket (`X-Basket-Token`) |
| POST | `/v1/baskets/{id}/items` | Add an item (`X-Basket-Token`) |
| PATCH | `/v1/baskets/{id}/items/{item_id}` | Update its quantity or amount |
| DELETE | `/v1/baskets/{id}/items/{item_id}` | Remove an item |
| POST | `/v1/baskets/{id}/quote` | Quote saved basket; body specifies optimization options |
| DELETE | `/v1/baskets/{id}` | Delete saved basket and all its lines |

All saved basket routes **except creation** require `X-Basket-Token: <secret>`, obtained once from the creation response. Only a SHA-256 hash is stored in SQLite. Treat the secret like a password; keep it in device secure storage and use HTTPS. A real mobile production service also needs account authentication, rate limiting, and basket cleanup policies.

## Stateless basket quote (easiest mobile integration)

```bash
curl -s -X POST http://localhost:8080/v1/basket/quote \
  -H 'Content-Type: application/json' \
  -d '{
    "items": [
      {"comparison_key":"milk|unspecified|uht|3.2|plain|normal","amount":2000,"unit":"ml"},
      {"comparison_key":"bread|white","amount":500,"unit":"g"}
    ],
    "allow_candidate_matches":true,
    "max_stores":2
  }'
```

The `demo/` catalog yields (MKD):

| Calculation | Result | Why |
| --- | ---: | --- |
| Tinex only | 203.00 | 2 × 1 L milk at 79 + one bread at 45 |
| KAM only | 210.00 | 1 × 2 L milk at 145 + one bread at 65 |
| Ramstore only | **175.00** | 2 × 1 L milk at 75 + one bread at 25 |
| Stokomak only | Incomplete | Bread available; milk missing |
| Cheapest split | **168.00** | Milk from KAM for 145 + bread from Stokomak for 23 |

**Savings:** 7.00 MKD compared with cheapest single complete store, excluding travel and membership discounts. These are synthetic examples, **not actual retailer prices**.

Response shape (abbreviated):

```json
{
  "currency": "MKD",
  "match_policy": "includes_unverified_comparison_groups",
  "freshness_limit_hours": 168,
  "cheapest_single_store": { "store_id": "ramstore-demo", "complete": true, "total_minor": 17500, "total_mkd": 175.0, "lines": [] },
  "cheapest_split_basket": { "complete": true, "total_minor": 16800, "total_mkd": 168.0, "stores": 2, "lines": [] },
  "savings_minor": 700,
  "store_options": [],
  "warnings": []
}
```

The actual API returns selected line items (name, store, pack count, physical amount, total, normalized MKD/l, MKD/kg or MKD/pc, freshness/availability, match quality), fully ranked store choices, missing item IDs, and warnings. If no store carries *all* items, `cheapest_single_store` is `null`, and no single-store total is fabricated. If a split cannot cover every item, `cheapest_split_basket.complete=false` and `total_minor`/`total_mkd` are `null`. Individual store's `partial_total_minor` must **never** be shown as a complete basket total.

Basket lines have exactly one of:

- `{"product_id":"<retail listing ID>","quantity":2}` — exact listing and number of retail packs; never substitutes brands unless explicit `allow_candidate_matches=true` and compatible package sizes are known.
- `{"comparison_key":"<group key>","amount":2000,"unit":"ml"}` — desired *physical amount*; offer needs `ceil(amount / pack_size)` whole packages. These matches require `allow_candidate_matches=true` explicitly. Works with `g`, `ml`, and `pcs`.

The app should offer a user-controlled switch between **same listing only** and **include candidate equivalent products**. Candidate matches are not verified identical SKUs and could include meaningful differences. For meat sold by variable weight and listings missing package/weight data, the API does not invent a price for a requested weight; such offers are excluded from quantity-based quotes.

### Quote options

- `markets`: array of allowed market codes, e.g. `["tinex","kam"]` (default any).
- `store_ids`: array of exact stores to consider (default any). For nearby stores, filter this list on the client until locations are part of the DB.
- `allow_candidate_matches`: `false` by default; must be `true` to use group matches.
- `include_unknown_availability`: `false` by default; unknown stock is excluded unless opted in. Explicit `available=false` is always excluded.
- `max_age_hours`: default 168 (one week); 0 disables the freshness filter. If a retailer supplies `source_updated_at`, that is used, otherwise ingestion time is used and a warning is returned. Supported dates: ISO, DD.MM.YYYY, DD/MM/YYYY.
- `max_stores`: 0 (no limit), 1, 2, or 3. With a limit, the solver enumerates exact store combinations up to three visits (max 40 eligible stores); no visit cap picks cheapest item by item.

There are no geocoordinates in the source schema yet, so the optimization does **not** assume two stores are geographically convenient. It does not include fuel, public transport, walking time, loyalty cards, delivery minimums, coupon eligibility, or inventory quantity. App prices are **indicative only**, may be stale, and are not checkout guarantees.

## Saved basket example

```bash
curl -X POST http://localhost:8080/v1/baskets
# Response: {"id":"...", "basket_token":"...", ...}

curl -X POST "http://localhost:8080/v1/baskets/$BASKET_ID/items" \
  -H "X-Basket-Token: $BASKET_TOKEN" -H 'Content-Type: application/json' \
  -d '{"comparison_key":"milk|unspecified|uht|3.2|plain|normal","amount":2000,"unit":"ml"}'

curl -X POST "http://localhost:8080/v1/baskets/$BASKET_ID/quote" \
  -H "X-Basket-Token: $BASKET_TOKEN" -H 'Content-Type: application/json' \
  -d '{"allow_candidate_matches":true,"max_stores":2}'
```

To adjust amounts: `PATCH /v1/baskets/{id}/items/{item_id}` with `{"amount":1000}`, or for SKU packs `{"quantity":3}`. The API never trusts the caller's `item_id` when creating a new basket item.

## Production limitations and next tasks

The underlying catalog collectors are unchanged from the first project: **Tinex/Ramstore are best-effort, unverified live HTML adapters; KAM/Stokomak are CSV feed sources, not fully automated crawlers.** A `sources.demo.json` demonstration does not make them real prices. Verify publisher terms, network accessibility, completeness, and price timestamps before public rollout.

High-priority extensions for a real consumer app: user accounts and authentication; verified GTIN/catalog mapping and human review; store address/latitude/longitude and radius filters; sale/promotion validity and membership-specific prices; actual weighed meat pricing; better Cyrillic category dictionaries; price availability monitoring; API pagination and caching; PostgreSQL once running multiple service replicas; rate limits/abuse defenses; full checkout/price disclaimers.

## Generate mobile clients from OpenAPI

The API includes OpenAPI 3.0.3 YAML/JSON and an interactive Swagger UI at `/docs`. Use the JSON at `http://localhost:8080/openapi.json` when generating typed clients. See [OPENAPI_INTEGRATION.md](OPENAPI_INTEGRATION.md) for copyable Flutter, Kotlin, Swift, and TypeScript generator commands and saved-basket authentication notes.
