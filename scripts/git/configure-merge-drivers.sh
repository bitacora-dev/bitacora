#!/usr/bin/env sh
# Configure the repository-local merge driver declared in .gitattributes.
set -eu

repo_root="${1:-$(git rev-parse --show-toplevel)}"

if ! git -C "$repo_root" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "not a Git worktree: $repo_root" >&2
  exit 1
fi

git -C "$repo_root" config merge.bitacora-ours-dist.name \
  "Keep the current branch's generated Web UI assets"
git -C "$repo_root" config merge.bitacora-ours-dist.driver \
  "scripts/git/merge-ours-dist.sh %O %A %B"

echo "configured merge driver bitacora-ours-dist for $repo_root"
