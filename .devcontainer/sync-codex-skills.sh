#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_skills="$script_dir/../.codex/skills"
target="${CODEX_HOME:-$HOME/.codex}/skills"

mkdir -p "$target"

if [ -d "$repo_skills" ]; then
  for d in "$repo_skills"/*; do
    [ -d "$d" ] || continue
    dest="$target/$(basename "$d")"
    rm -rf -- "$dest"
    ln -s "$d" "$dest"
  done
fi
