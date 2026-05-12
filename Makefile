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
	@case ":$$PATH:" in \
		*":$$HOME/.local/bin:"*) ;; \
		*) echo "⚠️  $$HOME/.local/bin not in PATH — installed browzer binary won't be found by your shell."; \
		   echo "    Add to your shell rc: export PATH=\"\$$HOME/.local/bin:\$$PATH\"" ;; \
	esac

mutate: ## Run go-mutesting against the validator + dispatch scope + any changed .go files (~30-60min)
	@command -v go-mutesting >/dev/null 2>&1 || (echo "go-mutesting not installed; run: go install github.com/avito-tech/go-mutesting/cmd/go-mutesting@latest" && exit 1)
	@mkdir -p mutate-out
	# Auto-discovery is best-effort: picks up changed non-test, non-schema files since the merge-base.
	# The static list below (./internal/schema/ + the two workflow files) is the authoritative floor —
	# auto-discovered files are additive extras. If git is unavailable or the cascade exhausts all
	# fallbacks, MUTATE_CHANGED_FILES is empty and only the static list runs.
	$(eval _MERGE_BASE := $(shell git merge-base HEAD origin/main 2>/dev/null || git merge-base HEAD upstream/main 2>/dev/null || echo HEAD~10))
	$(eval MUTATE_CHANGED_FILES := $(shell git diff $(_MERGE_BASE) --name-only -- 'packages/cli/*.go' 2>/dev/null \
		| sed 's|^packages/cli/||' \
		| grep -Ev '(_test\.go$$|^internal/schema/)' \
		| while IFS= read -r f; do [ -f "$$f" ] && echo "$$f"; done \
		|| true))
	@if [ -n "$(MUTATE_CHANGED_FILES)" ]; then \
		echo "Auto-discovered changed files: $(MUTATE_CHANGED_FILES)"; \
	fi
	go-mutesting --exec-timeout=120 ./internal/schema/ ./internal/commands/workflow_append_dispatch.go ./internal/commands/workflow_describe_step_type.go $(MUTATE_CHANGED_FILES) > mutate-out/report.txt 2>&1 || true
	@echo "Mutation report: packages/cli/mutate-out/report.txt"
