# Run Grocery Catalog API with Docker Desktop on Windows

This guide is for **Windows 10/11 with Docker Desktop**, PowerShell, and Docker Compose v2. The application itself runs as a **Linux container**; no Windows Go installation, SQLite server installation, or manual database setup is required.

## 1. Requirements

1. Install [Docker Desktop for Windows](https://docs.docker.com/desktop/setup/install/windows-install/). Start Docker Desktop and enable the **WSL 2 backend / Linux containers** (Settings > General, where applicable). If Docker says it is using Windows containers, switch to Linux containers.
2. Extract the ZIP to a normal writable folder, e.g. `C:\Projects\grocery-api`. The folder contains `compose.yaml`, `Dockerfile`, `sources.json`, and an `imports` directory. Keep them together.
3. Open **PowerShell** in the extracted `grocery-api` folder. Verify: `docker version` and `docker compose version`.

Use `docker compose` (with a space), not the obsolete `docker-compose` command. **Do not use Bash syntax** such as `ADMIN_TOKEN=... docker compose ...` in PowerShell.

## 2. First run: offline synthetic demo (recommended)

The demo loads four small **fictional** Tinex/KAM/Ramstore/Stokomak CSV price feeds bundled in the image. Unlike the live mode, it is usable without the retail websites or additional CSV files.

```powershell
docker compose -f compose.demo.yaml up --build -d
docker compose -f compose.demo.yaml ps
Invoke-RestMethod http://localhost:8081/healthz
Invoke-RestMethod 'http://localhost:8081/v1/products?category=milk'
Start-Process 'http://localhost:8081/docs'
```

The Go server starts immediately and imports in the background. Product listings may initially be empty; inspect status with:

```powershell
docker compose -f compose.demo.yaml logs --tail=100 api
Invoke-RestMethod 'http://localhost:8081/v1/sync-runs'
```

Run the included Windows smoke check, which checks health, OpenAPI, milk, and a cheapest-basket quote:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\windows-smoke.ps1
```

The demo uses **port 8081** and its own named SQLite volume, completely separate from the real-mode volume.

## 3. Start the normal application

From the same directory:

```powershell
# Optional: create a local configuration file for port/admin settings.
Copy-Item .env.example .env
notepad .env

docker compose up --build -d
docker compose ps
Invoke-RestMethod http://localhost:8080/healthz
Start-Process 'http://localhost:8080/docs'
```

This builds the Go image with SQLite and OpenAPI, persists data in the named Docker volume `grocery-catalog_grocery_data`, and starts a cron-driven collection job (default **03:00 Europe/Skopje**). No separate PostgreSQL or SQLite server is needed: SQLite is embedded inside the Go service and stored on the volume.

**Coverage limitations:** Vero/Jumbo use paginated published price-list crawlers; Kipper now has a separate physical-branch discovery/price-table adapter (`market=kipper`) and the experimental online catalog (`market=kipper-online`). Branch pages sometimes expose **no price rows** without JavaScript, so live physical-store coverage is unverified. Online Kipper and Žito-linked BigShop are **not** physical branch prices. Tinex and Ramstore are best-effort website parsers that still need validation against live HTML. Stokomak and Žito now crawl their official paginated store price lists automatically (the default fallback store IDs are 5 and 13). KAM crawls priced public promotions automatically but **not** its full store catalog. No CSV is needed for those three; inspect `/v1/sync-runs` to see real results. Real product counts can initially be zero. Check `/v1/sync-runs` and `/v1/stores` rather than assuming successful/complete collection.

## 4. Automatic price-list crawlers and optional CSV feeds

Place files in the same project's `imports` folder, e.g.:

- Stokomak and Žito: automatic `proverkanaceni.mk` crawlers, no files required.
- KAM: automatic partial public-offer crawler, no files required.
- Kipper: automatic branch and online product crawlers (subject to coverage limitations), no CSV required.
- Optional additional retailers: `C:\Projects\grocery-api\imports\othermarket.csv`

The required UTF-8 CSV headers are `name,price_mkd`; optional headers include `description,available,source_updated_at`. Example structure **with fictional prices**:

```csv
name,price_mkd,description,available
"TEST МЛЕКО 3.2% 1Л",70.00,"ТРАЈНО МЛЕКО",true
```

These files are mounted **read-only** to `/app/imports`; there is no Windows drive-letter path inside the container. Restart not required when files are updated. The scheduled job will read them at the next cron time; you can trigger an admin sync sooner if an admin token is configured.

To add other chains/stores, edit `sources.json` and add source entries. The source configuration is **baked into the image** to avoid fragile Windows host-file mounts, so after changing `sources.json` run:

```powershell
docker compose up --build -d
```

## 5. Basket comparison example (PowerShell)

The following example assumes the **demo** is running on port 8081. These are candidate equivalent products, not verified identical products.

```powershell
$payload = @{
  items = @(
    @{ comparison_key = 'milk|unspecified|uht|3.2|plain|normal'; amount = 2000; unit = 'ml' },
    @{ comparison_key = 'bread|white'; amount = 500; unit = 'g' }
  )
  allow_candidate_matches = $true
  max_stores = 2
} | ConvertTo-Json -Depth 10

Invoke-RestMethod -Method Post `
  -Uri 'http://localhost:8081/v1/basket/quote' `
  -ContentType 'application/json' `
  -Body $payload
```

Production mode uses `http://localhost:8080`; its offers may be empty until you have valid sources.

## 6. Stop, update, and keep your data

```powershell
# Live service
docker compose logs -f api
docker compose down                   # Stop/delete container and network; KEEP data volume
docker compose up --build -d          # Rebuild/update and reuse data

# Demo service
docker compose -f compose.demo.yaml down
```

**Do not run `docker compose down --volumes`** unless you intentionally want to delete stored product history/baskets. The SQLite file and its WAL files reside together inside a Docker-managed named volume. For production backups, stop the API before copying or archiving that volume (or use the SQLite Online Backup API via a suitable tool); never copy a live WAL database file alone.

## 7. Mobile device access

By default the app listens on **Windows localhost only**, for safer local development. Android emulators (depending on emulator) may use `http://10.0.2.2:8080` to reach the host; physical phones cannot use `localhost` to access your Windows machine. To allow local-network access, edit `.env`:

```ini
API_BIND_ADDRESS=0.0.0.0
API_PORT=8080
```

Then run `docker compose up -d` and use `http://<YOUR-WINDOWS-LAN-IP>:8080` from a phone on the same trusted network. You may need a Windows Defender Firewall inbound rule. Do **not** expose it on the public Internet without HTTPS, authentication, traffic limits, and a deployment security review. For a device outside your local Wi-Fi, deploy the service behind HTTPS instead.

## 8. Troubleshooting

| Symptom | Fix |
|---|---|
| `docker` not recognized | Install/start Docker Desktop; open a new PowerShell window. |
| Daemon not running / Windows image build fails | In Docker Desktop switch to Linux containers, enable WSL 2, and restart Docker Desktop. |
| Port 8080/8081 in use | Edit `.env` (`API_PORT=8090`, `DEMO_PORT=8091`) and restart; adjust URLs. |
| Container is unhealthy | `docker compose logs --tail=100 api`; check `/healthz`, disk space, and source config. |
| Empty `/v1/products` | Look at `/v1/sync-runs`; live sources are not guaranteed to provide complete working catalogs. Try the demo first. |
| `imports/*.csv` missing | Only explicitly configured CSV sources for other markets require files; KAM, Stokomak, Žito and Kipper use automatic crawlers. Missing files fail visibly, they do not fabricate products. |
| File sharing / mount denied | Move project to a local Windows user folder or WSL filesystem with Docker Desktop integration enabled; ensure `imports/` exists. |
| PowerShell `curl` behaves unexpectedly | Use `Invoke-RestMethod`, as shown above. |
| Swagger page is blank while API works | `/docs` downloads JS/CSS from the Internet; `/openapi.json` and `/openapi.yaml` work locally. |

See [README.md](README.md), [MOBILE_API.md](MOBILE_API.md), and [OPENAPI_INTEGRATION.md](OPENAPI_INTEGRATION.md) for API details.

## 10. Account login and additional crawlers

The demo and real Compose services now include account registration, login, logout, account-specific saved baskets, and OpenAPI documentation of these endpoints. Run `scripts\windows-smoke.ps1` to test the full registration + basket + login flow. **No SMTP/email verification or password-reset functionality is included.** For an internet-facing deployment, add an HTTPS reverse proxy, anti-abuse controls, and secure mobile token storage. Ports remain localhost-only by default.

`sources.json` now configures the Vero/Jumbo live price-list directory, plus experimental online price-only sources for Kipper and BigShop (linked from Žito's site). Optional CSV sources still report missing-file errors; Kipper is also automated but branch rows may be dynamically loaded. To avoid slow first-time live collection, start with `compose.demo.yaml`. See `AUTH_AND_CRAWLERS.md` for accurate per-source coverage and example PowerShell account requests.


## Latest live-source change

As of October 16, 2026, the production `sources.json` automatically crawls **Stokomak**, **Žito**, and the **KAM promotional offers**. No Windows-managed CSV files are required for these three. Rebuild with `docker compose up --build -d`, and inspect:

```powershell
Invoke-RestMethod http://localhost:8080/v1/sync-runs
Invoke-RestMethod 'http://localhost:8080/v1/products?market=zito&limit=20'
Invoke-RestMethod 'http://localhost:8080/v1/products?market=stokomak&limit=20'
Invoke-RestMethod 'http://localhost:8080/v1/products?market=kam&limit=20'
```

**KAM is partial**: those listings must not be counted as a complete basket at KAM. Stokomak and Žito catalog pages are per store; menu-based discovery may need further verification with a real browser. Use `/v1/sync-runs` to see if only the fallback branch was used. The project has been tested with local Go fixtures, not live website requests or Windows Docker Desktop.

## Kipper branch crawler

The live `sources.json` enables `kipper_directory`, which discovers public market pages and then uses `kipper_store` for the physical branch product tables. Verify branch ingestion using:

```powershell
Invoke-RestMethod 'http://localhost:8080/v1/products?market=kipper'
Invoke-RestMethod 'http://localhost:8080/v1/products?market=kipper-online'
Invoke-RestMethod 'http://localhost:8080/v1/sync-runs'
```

The online feed is separate and not valid for in-store basket totals. Some Kipper branch pages publish headings but populate actual products dynamically: the crawler fails safely on these pages; see [KIPPER_CRAWLER.md](KIPPER_CRAWLER.md).
