# Accounts and additional retailer adapters

## Windows Docker quickstart

Run Docker Desktop with WSL 2/Linux containers. In PowerShell:

```powershell
docker compose -f compose.demo.yaml up --build -d
Start-Process http://localhost:8081/docs
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\windows-smoke.ps1
```

The demo has fabricated offers, including demo milk and bread, in a separate persistent SQLite volume. For real configured sources, `docker compose up --build -d` and use port 8080. No external Go, Python, or database installation is required on Windows.

## Register, login and authenticated baskets

`POST /v1/auth/register` accepts JSON `{"email":"someone@example.com","password":"a-long-unique-password"}`. It creates an account and returns a **seven-day opaque bearer token** and public user profile (no password hash). `POST /v1/auth/login` accepts the same shape and returns a new independent bearer token. `GET /v1/auth/me` and `POST /v1/auth/logout` require `Authorization: Bearer <access_token>`; logout revokes the current session. `GET /v1/me/baskets` lists the caller's saved baskets.

Example PowerShell against the synthetic demo:

```powershell
$api = 'http://localhost:8081'
$body = @{email='your-address@example.com'; password='replace-with-a-unique-long-password'} | ConvertTo-Json
$session = Invoke-RestMethod -Uri "$api/v1/auth/register" -Method Post -ContentType 'application/json' -Body $body
$headers = @{Authorization = "Bearer $($session.access_token)"}
$account = Invoke-RestMethod -Uri "$api/v1/auth/me" -Headers $headers
$basket = Invoke-RestMethod -Uri "$api/v1/baskets" -Method Post -Headers $headers
Invoke-RestMethod -Uri "$api/v1/me/baskets" -Headers $headers
Invoke-RestMethod -Uri "$api/v1/baskets/$($basket.id)" -Headers $headers
Invoke-RestMethod -Uri "$api/v1/auth/logout" -Method Post -Headers $headers
```

When a bearer token is provided to `POST /v1/baskets`, the new basket is tied to that user, and all subsequent `GET`, `PATCH`, `DELETE`, item, and quote operations require the **same account**. A different user receives 404 for the protected basket. No basket secret is returned for account-owned baskets.

**Legacy compatibility:** `POST /v1/baskets` without bearer token creates an anonymous basket and responds with an `X-Basket-Token` secret. Send that header for operations on such an anonymous basket. An authenticated user can transfer ownership to their account with `POST /v1/baskets/{id}/claim` using *both* the bearer token and the anonymous `X-Basket-Token`. After claim, the original anonymous token is invalid. This allows previously created mobile device baskets to survive account signup.

**Credentials and security:** Login passwords use per-user random 16-byte salts and 210,000-iteration PBKDF2-HMAC-SHA256 (standard-library-only implementation, not the preferred Argon2id alternative); bearer tokens are cryptographically random and stored only as SHA-256 digests. Session expiry and revocation are checked from SQLite. The app limits login and registration attempts in memory. Mobile apps must store bearer tokens securely in Keychain/Keystore, never in analytics, and deploy the API behind HTTPS and appropriate reverse-proxy request-size/rate controls before real public use. **Not yet implemented:** email verification, password reset, MFA, durable distributed rate limiting, and production-grade credential auditing. These are important before accepting public signups.

All auth endpoints and the dual ownership schemes are described in `/docs`, `/openapi.json` and `/openapi.yaml` for SDK generation.

## Additional crawlers and actual coverage

| Market/source | Configured adapter | Coverage | What to expect |
|---|---|---|---|
| Tinex | `tinex` | Store-specific, paginated HTML | Existing adapter; live verification required |
| Ramstore | `ramstore_html`, `ramstore_directory` | Store-specific HTML and directory | Existing adapters; live verification required |
| **Vero / Jumbo** | **`vero_html`, `vero_directory`** | **Store-specific paginated published price lists** | New adapter follows all linked pages and discovers separate stores |
| **Kipper physical stores** | **`kipper_directory` + `kipper_store`** | **Best-effort store HTML price tables; live rows unverified** | Directory may omit branches; blank JS-populated tables fail closed |
| **Kipper online** | **`woo_shop`** | **Online-priced listings only; experimental and incomplete** | Separate market code `kipper-online`; not a physical-store price source |
| **BigShop (Žito-linked online)** | **`woo_shop`** | **Online-priced listings only; experimental and incomplete** | Not a physical Žito branch price source |
| **KAM** | **`kam_offers`** | **Partial** public priced promotions | Automated; not a full physical-store catalog |
| **Stokomak** | **`price_portal_directory`** | Official published store-level full price lists | HTML store discovery, fallback org=5; failures do not erase data |
| **Žito physical stores** | **`price_portal_directory`** | Official published store-level full price lists | HTML store discovery, fallback org=13; failures do not erase data |
| Kipper and other retailers | `csv` (optional) | External feeds | Žito physical prices no longer need CSV |

Public entry points (as researched October 2026):

- `https://pricelist.vero.com.mk/`: official Vero / Jumbo store list, linking to pages such as `89_1.html`, `89_2.html`.
- `https://kipper.mk/mk/shop/`: Kipper online shop. Many listings may have no visible price; the crawler will not invent prices.
- `https://bigshop.mk/shop/`: BigShop online retail shop, linked from `https://zito.com.mk/`; prices are not automatically valid at a Žito supermarket branch.

`vero_directory` follows actual discovered store links; `vero_html` uses correct Vero-specific column order (price, availability, description, regular price) and follows page N until no link to N+1 is present. If any page fails, its prior store snapshot remains unchanged. Store grouping includes Jumbo as its own market when the directory labels it so.

`woo_shop` is a **best-effort experimental public listing parser**, not a verified full catalog. It follows listing pagination, imports only cards with an explicit MKD price, and sets source status `partial`. Some retailer themes may use JavaScript or markup the adapter cannot parse and will show `error` instead of silently marking the catalog complete. It is intentionally configured as `kipper-online` or `bigshop-online` — never mixed with verified physical-store records. Sources are read from `sources.json` each time the image is built.

The application reads each site's `robots.txt`, honors disallow directives to the extent supported by the conservative parser, rate-limits requests, and does not evade logins or scraping restrictions. Check site terms, obtain any needed permission and monitor `/v1/sync-runs` before large production crawls. **These live adapters are not verified against real network responses in this offline build environment.** Their unit/integration tests use representative HTML fixtures; production HTML may change.

## Running tests / OpenAPI

With a local Go + CGO + SQLite installation:

```sh
go test ./... -count=1
go test -race ./...
go vet ./...
python scripts/generate_openapi.py  # only when editing the source contract
```

All 25 implemented HTTP operations are in OpenAPI 3.0.3. The Go code embeds the checked-in generated OpenAPI JSON/YAML. Use Swagger UI at `/docs`. Docker Compose demo and live deployments use separate volumes and localhost-only ports by default.


Stokomak and Žito now use the same official store-specific `proverkanaceni.mk` HTML paginator, with automatic product count verification. KAM uses its public priced offers automatically, but its price-list page has not yielded a confirmed full catalog. See README for explicit source coverage.
