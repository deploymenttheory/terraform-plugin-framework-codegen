#!/usr/bin/env bash
# Three structural rules CI enforces on every push:
#   1. No hand-written Go source file over 800 lines — decompose instead.
#      Generated and fixture code under testdata/ is exempt: it is produced,
#      not maintained, and reproducing it is cheap.
#   2. No pilot leakage: vendor names from proof APIs may not appear in
#      non-test source; a general toolkit must not ship one vendor's
#      constants as defaults. Test inputs are exempt -- testdata/, and
#      internal/vendor_openapi_specs, which exists to hold vendors'
#      documents and so must name them. A document is not a default.
#   3. No committed binaries: nothing tracked outside testdata/ may exceed
#      1 MiB. Test inputs are exempt: a vendor's OpenAPI document is
#      committed on purpose and runs to megabytes.
set -euo pipefail
failed=0

while read -r file; do
  lines="$(wc -l <"$file")"
  if (( lines > 800 )); then
    echo "repo_hygiene: $file is $lines lines, over the 800-line ceiling" >&2
    failed=1
  fi
done < <(git ls-files '*.go' | grep -v '^testdata/' | grep -v '/testdata/')

pattern='thousandeyes|jamfpro|msgraph|graph\.microsoft'
if hits="$(git ls-files '*.go' | grep -v '_test\.go$' | grep -v '^testdata/' | grep -v '/testdata/' \
    | grep -v '^internal/vendor_openapi_specs/' \
    | xargs -r grep -lniE "$pattern" 2>/dev/null)"; then
  if [[ -n "$hits" ]]; then
    echo "repo_hygiene: pilot vendor names found in non-test source:" >&2
    echo "$hits" >&2
    failed=1
  fi
fi

while read -r file; do
  size="$(wc -c <"$file")"
  if (( size > 1048576 )); then
    echo "repo_hygiene: $file is $size bytes — binaries and bulk artifacts are not committed" >&2
    failed=1
  fi
done < <(git ls-files | grep -v '^testdata/' | grep -v '/testdata/')

exit "$failed"
