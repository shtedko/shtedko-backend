.PHONY: run test build once docker
run:
	CGO_ENABLED=1 go run .
test:
	CGO_ENABLED=1 go test ./...
	CGO_ENABLED=1 go vet ./...
build:
	CGO_ENABLED=1 go build -o grocery-api .
once:
	CGO_ENABLED=1 go run . -once
docker:
	docker compose up --build -d

# Regenerate the checked-in OpenAPI YAML and JSON (requires Python 3 + PyYAML).
openapi:
	python3 scripts/generate_openapi.py

# Compare documented OpenAPI method/path pairs with actual ServeMux routes.
contract-test:
	CGO_ENABLED=1 go test ./... -run OpenAPI -count=1
