package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestOpenAPIMatchesServerRoutes(t *testing.T) {
	data, err := openAPIFiles.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.OpenAPI != "3.0.3" {
		t.Errorf("unexpected OpenAPI version: %q", contract.OpenAPI)
	}
	if len(contract.Paths) < 10 {
		t.Errorf("OpenAPI missing endpoints: %d paths", len(contract.Paths))
	}
	pathParameter := regexp.MustCompile(`\{[^}]+\}`)
	mux := (&API{}).routeMux()
	count := 0
	for template, methods := range contract.Paths {
		for method := range methods {
			target := pathParameter.ReplaceAllString(template, "contract-test")
			req := httptest.NewRequest(strings.ToUpper(method), target, nil)
			_, pattern := mux.Handler(req)
			want := strings.ToUpper(method) + " " + template
			if pattern != want {
				t.Errorf("documented %s is not registered: mux matched %q", want, pattern)
			}
			count++
		}
	}
	if count < 15 {
		t.Errorf("too few operations: %d", count)
	}
}

func TestOpenAPIDownloadsAndDocs(t *testing.T) {
	mux := (&API{}).Routes()
	for _, test := range []struct{ path, contentType string }{
		{"/openapi.json", "application/json"},
		{"/openapi.yaml", "application/yaml"},
		{"/docs", "text/html"},
	} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, test.path, nil))
		if rr.Code != 200 {
			t.Errorf("%s status %d", test.path, rr.Code)
		}
		if !strings.HasPrefix(rr.Header().Get("Content-Type"), test.contentType) {
			t.Errorf("%s content type %q", test.path, rr.Header().Get("Content-Type"))
		}
		if rr.Body.Len() < 100 {
			t.Errorf("%s returned an unexpectedly empty response", test.path)
		}
	}
}

// Fail the build if a new v1 handler is registered but not documented.
func TestOpenAPICoversAllV1Handlers(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`mux\.HandleFunc\("(GET|POST|PATCH|DELETE) (/v1/[^" ]+)"`)
	matches := re.FindAllStringSubmatch(string(source), -1)
	if len(matches) < 15 {
		t.Fatalf("cannot locate registered handlers in server.go: %d", len(matches))
	}
	b, err := openAPIFiles.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, m := range matches {
		if _, found := doc.Paths[m[2]][strings.ToLower(m[1])]; !found {
			t.Errorf("undocumented handler: %s %s", m[1], m[2])
		}
	}
}
