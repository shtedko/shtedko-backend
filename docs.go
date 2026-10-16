package main

import (
	"embed"
	"net/http"
)

//go:embed openapi.yaml openapi.json
var openAPIFiles embed.FS

func serveOpenAPIYAML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	b, err := openAPIFiles.ReadFile("openapi.yaml")
	if err != nil {
		http.Error(w, "OpenAPI unavailable", 500)
		return
	}
	_, _ = w.Write(b)
}

func serveOpenAPIJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	b, err := openAPIFiles.ReadFile("openapi.json")
	if err != nil {
		http.Error(w, "OpenAPI unavailable", 500)
		return
	}
	_, _ = w.Write(b)
}

// Swagger UI JS and CSS load from a public CDN; /openapi.* themselves are self-hosted
// and available offline. Do not expose /docs as an authenticated admin console.
const swaggerUIHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Grocery API | OpenAPI docs</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
<style>body { margin: 0; font-family: system-ui, sans-serif; } .notice { background: #fff4cf; padding: 12px 20px; color:#423b27; font-size:14px; }</style>
</head><body>
<div class="notice">Price feeds may be incomplete or stale. Product comparison groups are unverified candidates, not identical SKUs.</div>
<div id="swagger-ui"></div>
<script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>window.addEventListener('load',function(){SwaggerUIBundle({url:'/openapi.json',dom_id:'#swagger-ui',deepLinking:true,displayRequestDuration:true,persistAuthorization:false});});</script>
</body></html>`

func serveSwaggerUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write([]byte(swaggerUIHTML))
}
