.PHONY: ci test lint build vet mutate

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

mutate: ## Run go-mutesting against the validator + dispatch scope (~30-60min)
	@command -v go-mutesting >/dev/null 2>&1 || (echo "go-mutesting not installed; run: go install github.com/avito-tech/go-mutesting/cmd/go-mutesting@latest" && exit 1)
	@mkdir -p mutate-out
	go-mutesting --exec-timeout=120 ./internal/schema/ ./internal/commands/workflow_append_dispatch.go ./internal/commands/workflow_describe_step_type.go > mutate-out/report.txt 2>&1 || true
	@echo "Mutation report: packages/cli/mutate-out/report.txt"
