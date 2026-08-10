#!/usr/bin/env bash
# Three structural rules CI enforces on every push:
#   1. No Go source file over 800 lines — decompose instead (v1's 3,556-line
#      probe file is the cautionary tale).
#   2. No pilot leakage: vendor names from proof APIs may not appear in
#      non-test source; a general toolkit must not ship one vendor's
#      constants as defaults. Pinned test inputs under testdata/ are exempt.
#   3. No committed binaries: nothing tracked may exceed 1 MiB (v1 shipped a
#      34 MB build of itself at the repo root).
set -euo pipefail
failed=0

while read -r file; do
  lines="$(wc -l <"$file")"
  if (( lines > 800 )); then
    echo "repo_hygiene: $file is $lines lines, over the 800-line ceiling" >&2
    failed=1
  fi
done < <(git ls-files '*.go')

pattern='thousandeyes|jamfpro|msgraph|graph\.microsoft'
if hits="$(git ls-files '*.go' | grep -v '_test\.go$' | grep -v '^testdata/' | grep -v '/testdata/' \
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
done < <(git ls-files)

exit "$failed"
