#!/usr/bin/env bash
# packages/cli/scripts/ci-local.sh
#
# Mirrors 100% of what .github/workflows/ci.yml runs for the Browzer Go CLI.
# Run this before every `git push` that touches packages/cli/.
#
# Requirements:
#   - Go >= 1.25  (go.mod requires 1.25.0; older toolchains will error)
#   - golangci-lint v2.5.0  (auto-installed into $(go env GOPATH)/bin if missing)
#
# Usage:
#   cd packages/cli && bash scripts/ci-local.sh
#   # or via make:  cd packages/cli && make ci

set -euo pipefail

LINT_VERSION="v2.5.0"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${CLI_DIR}"

# ── 0. Go version guard ───────────────────────────────────────────────────────
GO_VERSION=$(go version 2>/dev/null | grep -oE 'go[0-9]+\.[0-9]+' | head -1)
GO_MAJOR=$(echo "${GO_VERSION}" | cut -d. -f1 | tr -d 'go')
GO_MINOR=$(echo "${GO_VERSION}" | cut -d. -f2)

if [ "${GO_MAJOR:-0}" -lt 1 ] || { [ "${GO_MAJOR}" -eq 1 ] && [ "${GO_MINOR:-0}" -lt 25 ]; }; then
  echo "ERROR: go.mod requires Go 1.25+. Found: $(go version 2>/dev/null || echo 'not installed')"
  echo "Install from https://go.dev/dl/"
  exit 1
fi

# ── 1. Deps ───────────────────────────────────────────────────────────────────
echo "==> go mod download"
go mod download

# ── 2. Vet (mirrors 'Vet' step in ci.yml) ─────────────────────────────────────
echo "==> go vet ./..."
go vet ./...

# ── 3. Test with race detector (mirrors 'Test' step) ─────────────────────────
echo "==> go test -race -count=1 ./..."
go test -race -count=1 ./...

# ── 4. Cross-compile for all 5 CI targets (mirrors 'Build all targets') ──────
echo "==> cross-compile checks"
TARGETS=(
  "darwin arm64"
  "darwin amd64"
  "linux arm64"
  "linux amd64"
  "windows amd64"
)
for TARGET in "${TARGETS[@]}"; do
  OS=$(echo "${TARGET}" | awk '{print $1}')
  ARCH=$(echo "${TARGET}" | awk '{print $2}')
  printf '    GOOS=%-8s GOARCH=%s\n' "${OS}" "${ARCH}"
  GOOS="${OS}" GOARCH="${ARCH}" go build -o /dev/null ./cmd/browzer
done

# ── 5. golangci-lint (mirrors 'lint' job; pins to the exact CI version) ─────
# `golangci-lint --version` prints `has version 2.5.0 built with ...` —
# the leading `v` is NOT present in the output, so normalize by stripping
# it from both sides before comparing.
LINT_BIN="$(go env GOPATH)/bin/golangci-lint"
WANT_VERSION="${LINT_VERSION#v}"
INSTALLED_VERSION=""
if [ -x "${LINT_BIN}" ]; then
  INSTALLED_VERSION=$("${LINT_BIN}" --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
fi

if [ "${INSTALLED_VERSION}" != "${WANT_VERSION}" ]; then
  echo "==> installing golangci-lint ${LINT_VERSION} (found: '${INSTALLED_VERSION:-none}')"
  curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
    | sh -s -- -b "$(go env GOPATH)/bin" "${LINT_VERSION}"
fi

echo "==> golangci-lint run --timeout=5m"
"${LINT_BIN}" run --timeout=5m

echo ""
echo "All checks passed. Safe to push."
