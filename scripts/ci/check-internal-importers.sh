#!/usr/bin/env bash
# Fails when an internal package has no production importer, and when an
# exception has become stale. The dependency data comes from go list, so
# comments and test-only imports do not count. Keep this Bash 3.2-compatible:
# macOS still ships it as /bin/bash.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

readonly EXCEPTIONS_FILE="scripts/ci/internal-package-exceptions.txt"
readonly MODULE_PATH="$(go list -m -f '{{.Path}}')"
readonly INTERNAL_PREFIX="${MODULE_PATH}/internal/"

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/bitacora-internal-importers.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

exceptions="$tmp_dir/exceptions"
internal_packages="$tmp_dir/internal-packages"
importers="$tmp_dir/importers"

if [[ ! -f "$EXCEPTIONS_FILE" ]]; then
  echo "missing internal package exceptions file: $EXCEPTIONS_FILE" >&2
  exit 1
fi

while IFS='|' read -r package reason; do
  package="${package//[[:space:]]/}"
  reason="${reason#"${reason%%[![:space:]]*}"}"

  [[ -z "$package" || "$package" == \#* ]] && continue
  if [[ ! "$package" =~ ^internal/.+ ]] || [[ -z "$reason" ]]; then
    echo "invalid exception in $EXCEPTIONS_FILE: every entry must be 'internal/package | reason'" >&2
    exit 1
  fi
  printf '%s\n' "${MODULE_PATH}/${package}" >>"$exceptions"
done < "$EXCEPTIONS_FILE"

sort -u "$exceptions" -o "$exceptions"

go list -f '{{.ImportPath}}' ./internal/... | sort -u >"$internal_packages"

while IFS= read -r record; do
  importer="${record%%$'\t'*}"
  imports="${record#*$'\t'}"
  [[ "$importer" == "$imports" ]] && imports=""

  for imported in $imports; do
    if grep -Fqx "$imported" "$internal_packages" && [[ "$importer" != "$imported" ]]; then
      printf '%s\n' "$imported"
    fi
  done
done < <(go list -f '{{.ImportPath}}{{"\t"}}{{join .Imports " "}}' ./...) | sort -u >"$importers"

violations=0
while IFS= read -r package; do
  if grep -Fqx "$package" "$exceptions"; then
    if grep -Fqx "$package" "$importers"; then
      echo "stale internal package exception has a production importer: ${package#"${MODULE_PATH}/"}"
      violations=$((violations + 1))
    fi
    continue
  fi
  if ! grep -Fqx "$package" "$importers"; then
    echo "internal package has no production importer: ${package#"${MODULE_PATH}/"}"
    violations=$((violations + 1))
  fi
done < "$internal_packages"

while IFS= read -r package; do
  if ! grep -Fqx "$package" "$internal_packages"; then
    echo "stale internal package exception no longer names an internal package: ${package#"${MODULE_PATH}/"}"
    violations=$((violations + 1))
  fi
done < "$exceptions"

if (( violations > 0 )); then
  echo "$violations internal package(s) without a production importer found"
  exit 1
fi

echo "every internal package has a production importer or a current documented exception"
