#!/usr/bin/env bash
# Fail when total statement coverage in a Go cover profile is below the floor in
# .github/coverage-baseline.txt, so a pull request cannot quietly make the
# project less tested than it was.
#
# The gate fails closed: it exits 2, never 0, for an absent profile or baseline,
# a baseline that is not a percentage, or a profile with no total line.
#
# The gate applies to the unit + protocol profile (`coverage.out`). Acceptance
# coverage is reported separately; it is environment-dependent and therefore not
# part of the v1 gate (design §10).
#
# Usage: scripts/coverage-gate.sh [profile] [baseline]
set -euo pipefail

profile="${1:-coverage.out}"
baseline="${2:-.github/coverage-baseline.txt}"

die() {
  echo "coverage-gate: $*" >&2
  exit 2
}

[[ -f "$profile" ]] || die "profile '$profile' not found; run 'make coverage' first"
[[ -f "$baseline" ]] || die "baseline '$baseline' not found"

floor="$(tr -d '[:space:]' < "$baseline")"
if [[ ! "$floor" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
  die "baseline '$baseline' is not a percentage: '$floor'"
fi
if ! awk -v f="$floor" 'BEGIN { exit !(f >= 0 && f <= 100) }'; then
  die "baseline '$baseline' is outside 0..100: '$floor'"
fi

total="$(go tool cover -func="$profile" | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')"
if [[ -z "$total" ]]; then
  die "profile '$profile' has no total line"
fi

echo "Total statement coverage: ${total}% (floor ${floor}%)"

if awk -v a="$total" -v f="$floor" 'BEGIN { exit !(a + 0 >= f + 0) }'; then
  echo "coverage-gate: PASS"
  exit 0
fi

echo "coverage-gate: FAIL — ${total}% is below the ${floor}% floor in $baseline" >&2
echo "Add tests for what you changed, or lower the floor deliberately in the same pull request." >&2
exit 1
