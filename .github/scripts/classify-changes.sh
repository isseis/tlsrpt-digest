#!/bin/bash
# Classify changed files and output has-code-changes, has-devcontainer-changes,
# has-integration-changes.
#
# Usage:
#   classify-changes.sh <file>   # read newline-separated filenames from <file>
#   classify-changes.sh          # read from stdin
#
# When GITHUB_OUTPUT is set, results are appended to that file (GitHub Actions
# format).  Otherwise they are printed to stdout.

set -euo pipefail

if [ $# -ge 1 ]; then
  changed_files=$(cat "$1")
else
  changed_files=$(cat)
fi

# has-code-changes: any file that is not docs-only / devcontainer / LICENSE
code_files=$(echo "$changed_files" | grep -vE '(\.md$|^docs/|^\.devcontainer/|^LICENSE$)' || true)
if [ -n "$code_files" ]; then
  has_code=true
else
  has_code=false
fi

# has-devcontainer-changes: any file under .devcontainer/
if echo "$changed_files" | grep -q '^\.devcontainer/'; then
  has_devcontainer=true
else
  has_devcontainer=false
fi

# has-integration-changes: Go sources, Makefile, GitHub Actions workflows,
# GitHub scripts, devcontainer config, or testdata.
# Including .github/scripts/ ensures that changes to this script itself
# trigger the integration-test job.
if echo "$changed_files" | grep -qE '(\.go$|^Makefile$|^\.github/workflows/|^\.github/scripts/|^\.devcontainer/|^testdata/)'; then
  has_integration=true
else
  has_integration=false
fi

output() {
  local key=$1 val=$2
  if [ -n "${GITHUB_OUTPUT:-}" ]; then
    echo "${key}=${val}" >> "$GITHUB_OUTPUT"
  else
    echo "${key}=${val}"
  fi
}

output has-code-changes        "$has_code"
output has-devcontainer-changes "$has_devcontainer"
output has-integration-changes  "$has_integration"
