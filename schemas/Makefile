# packages/cli/schemas/Makefile
#
# CUE workflow schema codegen pipeline. The CUE file is the SSOT;
# everything else is generated. CI runs `make ci-check` to assert that
# checked-in artifacts match a fresh codegen.
#
# Requirements:
#   - cue v0.15.0+ on PATH (`go install cuelang.org/go/cmd/cue@v0.15.0`)
#   - node v22+ on PATH (used by ../../scripts/cue-to-markdown.mjs)
#
# Targets:
#   all       — regenerate every artifact (run after editing workflow-v1.cue)
#   ci-check  — assert no drift between checked-in artifacts and fresh codegen
#   vet       — `cue vet` the schema + all valid/invalid fixtures
#   clean     — remove generated artifacts (keeps workflow-v1.cue + fixtures)

CUE          := cue
NODE         := node
SCHEMA       := workflow-v1.cue
EMBED_MIRROR := ../internal/schema/workflow-v1.cue
JSON_OUT     := workflow-v1.schema.json
GO_OUT       := cue_types_workflow_gen.go
MD_OUT       := ../../skills/references/workflow-schema.md
MD_RENDER    := ../../../scripts/cue-to-markdown.mjs

VALID_FIXTURES   := $(wildcard fixtures/valid/*.json)
INVALID_FIXTURES := $(wildcard fixtures/invalid/*.json)

.PHONY: all ci-check sync-embed vet vet-valid vet-invalid clean

all: $(JSON_OUT) $(GO_OUT) $(MD_OUT) sync-embed

# sync-embed copies the SSOT to the embed mirror used by the daemon
# validator. Invoked by `make all` and verified by `make ci-check`.
sync-embed: $(SCHEMA)
	@cp $(SCHEMA) $(EMBED_MIRROR)

$(JSON_OUT): $(SCHEMA)
	$(CUE) def --out openapi $(SCHEMA) > $@.tmp && mv $@.tmp $@

$(GO_OUT): $(SCHEMA)
	$(CUE) exp gengotypes . && [ -f $(GO_OUT) ] && touch $(GO_OUT)

$(MD_OUT): $(SCHEMA) $(MD_RENDER)
	$(NODE) $(MD_RENDER) $(SCHEMA) > $@.tmp && mv $@.tmp $@

vet: vet-valid vet-invalid
	@echo "vet: schema + all fixtures OK"

vet-valid: $(SCHEMA) $(VALID_FIXTURES)
	@$(CUE) vet $(SCHEMA)
	@for f in $(VALID_FIXTURES); do \
	  $(CUE) vet -E -c -d '#WorkflowV1' $(SCHEMA) "$$f" >/dev/null 2>&1 || \
	    { echo "FAIL: valid fixture $$f did not validate"; exit 1; }; \
	done
	@echo "vet-valid: $(words $(VALID_FIXTURES)) fixtures OK"

vet-invalid: $(SCHEMA) $(INVALID_FIXTURES)
	@for f in $(INVALID_FIXTURES); do \
	  if $(CUE) vet -E -c -d '#WorkflowV1' $(SCHEMA) "$$f" >/dev/null 2>&1; then \
	    echo "FAIL: invalid fixture $$f was accepted (should have errored)"; \
	    exit 1; \
	  fi; \
	done
	@echo "vet-invalid: $(words $(INVALID_FIXTURES)) fixtures correctly rejected"

ci-check: vet
	@cmp -s $(SCHEMA) $(EMBED_MIRROR) || \
	  { echo "FAIL: embed mirror $(EMBED_MIRROR) drifted from SSOT $(SCHEMA) — run 'make sync-embed'"; exit 1; }
	@$(CUE) def --out openapi $(SCHEMA) > $(JSON_OUT).new
	@diff -u $(JSON_OUT) $(JSON_OUT).new || \
	  { echo "FAIL: $(JSON_OUT) is out of date (run 'make all')"; rm $(JSON_OUT).new; exit 1; }
	@rm $(JSON_OUT).new
	@if [ -f $(GO_OUT) ]; then \
	  cp $(GO_OUT) $(GO_OUT).backup; \
	  $(CUE) exp gengotypes . 2>/dev/null || true; \
	  diff -u $(GO_OUT).backup $(GO_OUT) || \
	    { echo "FAIL: $(GO_OUT) is out of date (run 'make all')"; mv $(GO_OUT).backup $(GO_OUT); exit 1; }; \
	  rm -f $(GO_OUT).backup; \
	fi
	@$(NODE) $(MD_RENDER) $(SCHEMA) > $(MD_OUT).new
	@diff -u $(MD_OUT) $(MD_OUT).new || \
	  { echo "FAIL: $(MD_OUT) is out of date (run 'make all')"; rm $(MD_OUT).new; exit 1; }
	@rm $(MD_OUT).new
	@echo "ci-check: all generated artifacts in sync"

clean:
	@rm -f $(JSON_OUT) $(GO_OUT) $(MD_OUT)
	@echo "clean: removed generated artifacts"
