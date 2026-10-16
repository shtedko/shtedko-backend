# Windows PowerShell 5.1+ smoke test for the demo container.
# Run: powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\windows-smoke.ps1
param([int]$Port = 8081)
$ErrorActionPreference = 'Stop'
$base = "http://localhost:$Port"
Write-Host "Waiting for the demo API and imported milk products on $base ..."
$ready = $false
for ($attempt = 0; $attempt -lt 45; $attempt++) {
    try {
        $health = Invoke-RestMethod -Uri "$base/healthz" -Method Get -TimeoutSec 4
        $milk = Invoke-RestMethod -Uri "$base/v1/products?category=milk&limit=20" -Method Get -TimeoutSec 4
        if ($health.status -eq 'ok' -and $milk.total -gt 0) {
            $ready = $true
            break
        }
    } catch {
        # API container may still be starting or the demo importer may be running.
    }
    Start-Sleep -Seconds 1
}
if (-not $ready) {
    throw 'Demo API did not populate products. Inspect: docker compose -f compose.demo.yaml logs --tail=100 api'
}
Write-Host "PASS: healthz OK, milk products=$($milk.total)"
$spec = Invoke-RestMethod -Uri "$base/openapi.json" -Method Get
if (-not $spec.openapi) { throw 'OpenAPI schema missing' }
Write-Host "PASS: OpenAPI $($spec.openapi)"

$payload = @{
    items = @(
        @{ comparison_key = 'milk|unspecified|uht|3.2|plain|normal'; amount = 2000; unit = 'ml' },
        @{ comparison_key = 'bread|white'; amount = 500; unit = 'g' }
    )
    allow_candidate_matches = $true
    max_stores = 2
} | ConvertTo-Json -Depth 10
$quote = Invoke-RestMethod -Uri "$base/v1/basket/quote" -Method Post -ContentType 'application/json' -Body $payload
if (-not $quote.cheapest_single_store.complete) { throw 'Expected a complete single-store basket in the demo' }
if (-not $quote.cheapest_split_basket.complete) { throw 'Expected a complete split basket in the demo' }
Write-Host "PASS: single-store MKD $($quote.cheapest_single_store.total_mkd), split MKD $($quote.cheapest_split_basket.total_mkd)"
Write-Host "Swagger: $base/docs"
Write-Host 'All checks passed. Prices are DEMO ONLY, not actual supermarket prices.'

# Exercise the user-account flow in the real HTTP server. Never use these
# synthetic smoke credentials for a real account.
$email = "smoke-$([guid]::NewGuid().ToString('N'))@example.test"
$password = 'Smoke-test-password-123456789'
$credentials = @{ email = $email; password = $password } | ConvertTo-Json
$registration = Invoke-RestMethod -Uri "$base/v1/auth/register" -Method Post -ContentType 'application/json' -Body $credentials
if (-not $registration.access_token) { throw 'Registration did not return access token' }
$authHeaders = @{ Authorization = "Bearer $($registration.access_token)" }
$account = Invoke-RestMethod -Uri "$base/v1/auth/me" -Headers $authHeaders
if ($account.email -ne $email) { throw 'Current account email mismatch' }
$ownedBasket = Invoke-RestMethod -Uri "$base/v1/baskets" -Method Post -Headers $authHeaders
if ($ownedBasket.owner -ne 'account') { throw 'Basket was not account-owned' }
$myBaskets = Invoke-RestMethod -Uri "$base/v1/me/baskets" -Headers $authHeaders
if (@($myBaskets.items).Count -lt 1) { throw 'Account basket list is empty' }
Invoke-RestMethod -Uri "$base/v1/auth/logout" -Method Post -Headers $authHeaders | Out-Null
$loggedIn = Invoke-RestMethod -Uri "$base/v1/auth/login" -Method Post -ContentType 'application/json' -Body $credentials
if (-not $loggedIn.access_token) { throw 'Login failed after logout' }
Write-Host 'PASS: account register, bearer session, private basket, logout and login'
