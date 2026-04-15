.PHONY: ci test lint build vet

# Full CI parity — run before every push touching packages/cli/.
# Mirrors .github/workflows/ci.yml: vet + test-race + 5 cross-compiles + golangci-lint v2.5.0.
ci:
	bash scripts/ci-local.sh

# Fast feedback during development.
test:
	go test -race -count=1 ./...

vet:
	go vet ./...

lint:
	golangci-lint run --timeout=5m

# Install the binary locally (same target path used in CLAUDE.md dev docs).
build:
	go build -o "$$HOME/.local/bin/browzer" ./cmd/browzer
