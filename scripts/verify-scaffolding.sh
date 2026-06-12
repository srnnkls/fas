#!/usr/bin/env bash
#
# verify-scaffolding.sh
#
# Asserts the invariants of T1 project scaffolding for the `fas` repo.
# Exits non-zero on the first failed assertion so this script can gate CI
# and serve as a regression check against future structural drift.
#
# Invariants asserted:
#   - go.mod at repo root
#   - Package tree: cmd/fas, internal/{adapter,config,evaluator,parser,synthesis}
#   - Each package directory contains >= 1 .go file
#   - mise.toml at repo root
#   - mise exposes `build`, `test`, `lint` tasks
#   - CI workflow file under .github/workflows/
#   - CGO_ENABLED=0 enforced in mise.toml or .envrc
#   - `CGO_ENABLED=0 go build ./...` succeeds
#   - `CGO_ENABLED=0 go vet ./...` succeeds
#
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/.." && pwd)
cd "${REPO_ROOT}"

failures=0

pass() {
  printf 'OK:   %s\n' "$1"
}

fail() {
  printf 'FAIL: %s\n' "$1" >&2
  failures=$((failures + 1))
}

# --- go.mod ------------------------------------------------------------------

if [[ -f "${REPO_ROOT}/go.mod" ]]; then
  pass "go.mod present at repo root"
else
  fail "go.mod missing at repo root"
fi

# --- directory tree ----------------------------------------------------------

required_dirs=(
  "cmd/fas"
  "internal/adapter"
  "internal/config"
  "internal/evaluator"
  "internal/parser"
  "internal/synthesis"
)

for dir in "${required_dirs[@]}"; do
  if [[ -d "${REPO_ROOT}/${dir}" ]]; then
    pass "directory present: ${dir}"
  else
    fail "directory missing: ${dir}"
  fi
done

# --- at least one .go file in each package directory ------------------------

for dir in "${required_dirs[@]}"; do
  if [[ ! -d "${REPO_ROOT}/${dir}" ]]; then
    continue
  fi
  shopt -s nullglob
  go_files=("${REPO_ROOT}/${dir}"/*.go)
  shopt -u nullglob
  if (( ${#go_files[@]} > 0 )); then
    pass "package has .go files: ${dir}"
  else
    fail "package has no .go files: ${dir}"
  fi
done

# --- mise.toml --------------------------------------------------------------

build_file=""
if [[ -f "${REPO_ROOT}/mise.toml" ]]; then
  build_file="mise.toml"
  pass "mise.toml present at repo root"
else
  fail "mise.toml missing at repo root"
fi

# --- build/test/lint tasks reachable ----------------------------------------

check_target() {
  local target=$1
  if command -v mise >/dev/null 2>&1; then
    if mise tasks info "${target}" >/dev/null 2>&1; then
      pass "mise task reachable: ${target}"
    else
      fail "mise task missing or errors: ${target}"
    fi
  else
    fail "mise not installed; cannot verify task: ${target}"
  fi
}

for target in build test lint; do
  check_target "${target}"
done

# --- CI workflow ------------------------------------------------------------

shopt -s nullglob
workflows=("${REPO_ROOT}/.github/workflows"/*.yml "${REPO_ROOT}/.github/workflows"/*.yaml)
shopt -u nullglob
if (( ${#workflows[@]} > 0 )); then
  pass "CI workflow file present under .github/workflows/"
else
  fail "no CI workflow file under .github/workflows/"
fi

# --- CGO_ENABLED=0 enforcement ----------------------------------------------

cgo_enforced=0
cgo_sources=()

if [[ -f "${REPO_ROOT}/mise.toml" ]]; then
  if grep -Eq 'CGO_ENABLED[[:space:]]*=[[:space:]]*"?0"?|CGO_ENABLED=0' "${REPO_ROOT}/mise.toml"; then
    cgo_enforced=1
    cgo_sources+=("mise.toml")
  fi
fi

if [[ -f "${REPO_ROOT}/.envrc" ]]; then
  if grep -Eq 'CGO_ENABLED[[:space:]]*=[[:space:]]*0|export[[:space:]]+CGO_ENABLED=0' "${REPO_ROOT}/.envrc"; then
    cgo_enforced=1
    cgo_sources+=(".envrc")
  fi
fi

if (( cgo_enforced == 1 )); then
  pass "CGO_ENABLED=0 enforced in: ${cgo_sources[*]}"
else
  fail "CGO_ENABLED=0 not enforced in mise.toml or .envrc"
fi

# --- CGO_ENABLED=0 go build ./... -------------------------------------------

if command -v go >/dev/null 2>&1; then
  if CGO_ENABLED=0 go build ./... >/dev/null 2>&1; then
    pass "CGO_ENABLED=0 go build ./... succeeds"
  else
    fail "CGO_ENABLED=0 go build ./... failed"
  fi

  if CGO_ENABLED=0 go vet ./... >/dev/null 2>&1; then
    pass "CGO_ENABLED=0 go vet ./... succeeds"
  else
    fail "CGO_ENABLED=0 go vet ./... failed"
  fi
else
  fail "go toolchain not installed; cannot run build/vet"
fi

# --- summary -----------------------------------------------------------------

printf -- '----\n'
if (( failures == 0 )); then
  printf 'scaffolding verification: PASS\n'
  exit 0
else
  printf 'scaffolding verification: FAIL (%d assertion(s) failed)\n' "${failures}" >&2
  exit 1
fi
