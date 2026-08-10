#!/usr/bin/env bash
# The coverage gate is not advisory: total below TOTAL_MIN or any package
# below PKG_MIN fails the build. Reads the profile written by `make test`.
set -euo pipefail

profile="${1:-coverage.out}"
TOTAL_MIN="${TOTAL_MIN:-90}"
PKG_MIN="${PKG_MIN:-80}"

if [[ ! -f "$profile" ]]; then
  echo "coverage_gate: no profile at $profile — run the tests first" >&2
  exit 1
fi

total="$(go tool cover -func="$profile" | tail -1 | awk '{gsub(/%/, "", $3); print $3}')"

# Aggregate covered/total statements per package directory from the raw
# profile: each line after the mode header is file:start,end numstmts count.
per_pkg="$(tail -n +2 "$profile" | awk -F'[: ]' '
  {
    pkg = $1; sub(/\/[^\/]+$/, "", pkg)
    stmts[pkg] += $(NF-1)
    if ($NF > 0) covered[pkg] += $(NF-1)
  }
  END { for (p in stmts) printf "%s %.1f\n", p, 100 * covered[p] / stmts[p] }
' | sort)"

failed=0
while read -r pkg pct; do
  printf "%6s%%  %s\n" "$pct" "$pkg"
  if awk -v p="$pct" -v min="$PKG_MIN" 'BEGIN { exit !(p < min) }'; then
    echo "coverage_gate: $pkg is at ${pct}%, below the ${PKG_MIN}% package floor" >&2
    failed=1
  fi
done <<<"$per_pkg"

printf "%6s%%  total\n" "$total"
if awk -v p="$total" -v min="$TOTAL_MIN" 'BEGIN { exit !(p < min) }'; then
  echo "coverage_gate: total coverage ${total}% is below the ${TOTAL_MIN}% gate" >&2
  failed=1
fi

exit "$failed"
