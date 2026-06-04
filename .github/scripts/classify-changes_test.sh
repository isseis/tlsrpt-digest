#!/bin/bash
# Tests for classify-changes.sh
set -euo pipefail

SCRIPT="$(dirname "$0")/classify-changes.sh"
PASS=0
FAIL=0

run_case() {
  local desc="$1"
  local input="$2"
  local want_code="$3"
  local want_dc="$4"
  local want_int="$5"

  output=$(echo "$input" | bash "$SCRIPT")
  got_code=$(echo "$output"    | grep '^has-code-changes='        | cut -d= -f2)
  got_dc=$(echo "$output"      | grep '^has-devcontainer-changes=' | cut -d= -f2)
  got_int=$(echo "$output"     | grep '^has-integration-changes='  | cut -d= -f2)

  local ok=true
  if [ "$got_code" != "$want_code" ]; then
    echo "FAIL [$desc] has-code-changes: want=$want_code got=$got_code"
    ok=false
  fi
  if [ "$got_dc" != "$want_dc" ]; then
    echo "FAIL [$desc] has-devcontainer-changes: want=$want_dc got=$got_dc"
    ok=false
  fi
  if [ "$got_int" != "$want_int" ]; then
    echo "FAIL [$desc] has-integration-changes: want=$want_int got=$got_int"
    ok=false
  fi

  if $ok; then
    echo "PASS [$desc]"
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
  fi
}

run_case "Go source"             "internal/imap/client.go"              true  false true
run_case "Makefile"              "Makefile"                              true  false true
run_case "CI workflow"           ".github/workflows/ci.yml"             true  false true
run_case "classify-changes.sh"  ".github/scripts/classify-changes.sh"  true  false true
run_case "devcontainer"         ".devcontainer/docker-compose.base.yml" false true  true
run_case "testdata"             "testdata/tlsrpt_google.eml"            true  false true
run_case "go.mod"               "go.mod"                                true  false true
run_case "go.sum"               "go.sum"                                true  false true
run_case "docs only"            "docs/overview.md"                      false false false
run_case "LICENSE only"         "LICENSE"                               false false false

echo ""
echo "Results: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ]
