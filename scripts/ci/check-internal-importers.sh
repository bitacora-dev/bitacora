#!/usr/bin/env bash
# Fails when an internal package has no production importer. The dependency
# data comes from go list, so comments and test-only imports do not count.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

readonly EXCEPTIONS_FILE="scripts/ci/internal-package-exceptions.txt"
readonly MODULE_PATH="$(go list -m -f '{{.Path}}')"
readonly INTERNAL_PREFIX="${MODULE_PATH}/internal/"

declare -A exceptions=()

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
  exceptions["${MODULE_PATH}/${package}"]=1
done < "$EXCEPTIONS_FILE"

declare -A internal_packages=()
declare -A importers=()

while IFS= read -r package; do
  internal_packages["$package"]=1
done < <(go list -f '{{.ImportPath}}' ./internal/...)

while IFS= read -r record; do
  importer="${record%%$'\t'*}"
  imports="${record#*$'\t'}"
  [[ "$importer" == "$imports" ]] && imports=""

  for imported in $imports; do
    if [[ -n "${internal_packages[$imported]+present}" ]] && [[ "$importer" != "$imported" ]]; then
      importers["$imported"]=1
    fi
  done
done < <(go list -f '{{.ImportPath}}{{"\t"}}{{join .Imports " "}}' ./...)

violations=0
for package in "${!internal_packages[@]}"; do
  if [[ -n "${importers[$package]+present}" ]] || [[ -n "${exceptions[$package]+present}" ]]; then
    continue
  fi
  echo "internal package has no production importer: ${package#"${MODULE_PATH}/"}"
  violations=$((violations + 1))
done

if (( violations > 0 )); then
  echo "$violations internal package(s) without a production importer found"
  exit 1
fi

echo "every internal package has a production importer or a documented exception"
