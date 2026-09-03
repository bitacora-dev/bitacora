#!/usr/bin/env bash
set -euo pipefail

module_path="$(go list -m)"
repo_root="$(git rev-parse --show-toplevel)"

license_for_dir() {
  local dir="$1"
  while [[ "$dir" == "$repo_root"* ]]; do
    if [[ -f "$dir/LICENSE" ]]; then
      if grep -q "Apache License" "$dir/LICENSE"; then
        printf 'Apache-2.0\n'
        return 0
      fi
      if grep -q "GNU AFFERO GENERAL PUBLIC LICENSE" "$dir/LICENSE"; then
        printf 'AGPL-3.0\n'
        return 0
      fi
      printf 'unknown\n'
      return 0
    fi
    [[ "$dir" != "$repo_root" ]] || break
    dir="$(dirname "$dir")"
  done
  printf 'unknown\n'
}

package_dir() {
  go list -f '{{.Dir}}' "$1" 2>/dev/null
}

failed=0

while IFS=$'\t' read -r import_path dir imports; do
  [[ "$(license_for_dir "$dir")" == "Apache-2.0" ]] || continue
  [[ -n "$imports" ]] || continue

  while IFS= read -r imported; do
    [[ "$imported" == "$module_path/"* ]] || continue
    imported_dir="$(package_dir "$imported")"
    if [[ -z "$imported_dir" ]]; then
      printf 'license boundary: cannot resolve internal import %s from %s\n' "$imported" "$import_path" >&2
      failed=1
      continue
    fi
    imported_license="$(license_for_dir "$imported_dir")"
    if [[ "$imported_license" != "Apache-2.0" ]]; then
      printf 'license boundary: Apache-2.0 package %s imports %s (%s)\n' "$import_path" "$imported" "$imported_license" >&2
      failed=1
    fi
  done <<<"${imports//$' '/$'\n'}"
done < <(go list -f '{{.ImportPath}}{{"\t"}}{{.Dir}}{{"\t"}}{{join .Imports " "}}' ./...)

exit "$failed"
