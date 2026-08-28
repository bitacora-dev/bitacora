#!/usr/bin/env bash
# Fails if "os/exec" is imported outside the helper packages and
# bitacora-run — the only places ADR-0012 allows it. Test files are exempt:
# tooling (building/running a binary to test it) is not the production
# read-only constraint this checks.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

ALLOWED_PREFIXES=(
  "cmd/bitacora-smart/"
  "internal/smarthelper/"
)

violations=0

while IFS= read -r -d '' file; do
  rel="${file#./}"
  allowed=false
  for prefix in "${ALLOWED_PREFIXES[@]}"; do
    case "$rel" in
      "$prefix"*) allowed=true ;;
    esac
  done

  if [ "$allowed" = false ] && grep -q '"os/exec"' "$file"; then
    echo "forbidden os/exec import in $file (ADR-0012: only helpers and bitacora-run may exec)"
    violations=$((violations + 1))
  fi
done < <(find . -name '*.go' ! -name '*_test.go' -not -path './.git/*' -print0)

if [ "$violations" -gt 0 ]; then
  echo "$violations forbidden os/exec import(s) found"
  exit 1
fi

echo "no forbidden os/exec imports"
