#!/bin/sh
set -eu

repo_skills="/workspaces/ubuntu/.codex/skills"
target="${CODEX_HOME:-$HOME/.codex}/skills"

mkdir -p "$target"

if [ -d "$repo_skills" ]; then
  for d in "$repo_skills"/*; do
    [ -d "$d" ] || continue
    ln -sfn "$d" "$target/$(basename "$d")"
  done
fi
