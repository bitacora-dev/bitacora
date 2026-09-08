#!/usr/bin/env bash
# Proves the versioned setup script resolves a generated dist conflict by
# retaining the current branch's artifact. The temporary repository is removed.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/bitacora-dist-merge.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

fixture="$tmp_dir/repo"
mkdir -p "$fixture/scripts/git" "$fixture/internal/webui/dist"
cp "$repo_root/.gitattributes" "$fixture/.gitattributes"
cp "$repo_root/scripts/git/merge-ours-dist.sh" "$fixture/scripts/git/"
cp "$repo_root/scripts/git/configure-merge-drivers.sh" "$fixture/scripts/git/"
chmod +x "$fixture/scripts/git/merge-ours-dist.sh" \
  "$fixture/scripts/git/configure-merge-drivers.sh"

git init -q "$fixture"
git -C "$fixture" config user.name "Bitacora CI"
git -C "$fixture" config user.email "ci@example.invalid"

printf 'base\n' >"$fixture/internal/webui/dist/app.js"
git -C "$fixture" add .
git -C "$fixture" commit -qm "test: add generated asset"
base_branch="$(git -C "$fixture" branch --show-current)"

git -C "$fixture" checkout -qb regenerated
printf 'regenerated\n' >"$fixture/internal/webui/dist/app.js"
git -C "$fixture" commit -am "test: regenerate asset" -q

git -C "$fixture" checkout -q "$base_branch"
printf 'current branch\n' >"$fixture/internal/webui/dist/app.js"
git -C "$fixture" commit -am "test: update current artifact" -q

"$fixture/scripts/git/configure-merge-drivers.sh" "$fixture" >/dev/null
git -C "$fixture" merge --no-edit regenerated >/dev/null

test "$(cat "$fixture/internal/webui/dist/app.js")" = "current branch"
test -z "$(git -C "$fixture" ls-files --unmerged)"
git -C "$fixture" diff --check

echo "generated dist merge driver keeps the current branch artifact cleanly"
