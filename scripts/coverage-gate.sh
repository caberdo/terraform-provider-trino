#!/usr/bin/env bash
# Fail when statement coverage is below the floors in .github/, so a pull
# request cannot quietly make the project less tested than it was.
#
# Two checks run against the unit + protocol profile (`coverage.out`):
#
#   1. the module total against .github/coverage-baseline.txt (the backstop);
#   2. every package returned by `go list ./internal/...` against its floor in
#      .github/coverage-floors.txt (or the file's `default` floor). Packages are
#      discovered, not hardcoded, so a new resource or data-source package is
#      covered as soon as it exists.
#
# The gate fails closed: it exits 2, never 0, for an absent profile, baseline
# or floor file, a floor that is not a percentage, a floor that names a package
# `go list` does not return, or a profile with no total line.
#
# Acceptance coverage is reported separately; it is environment-dependent and
# therefore not part of the v1 gate (design §10).
#
# Usage: scripts/coverage-gate.sh [profile] [baseline] [floors]
set -euo pipefail

profile="${1:-coverage.out}"
baseline="${2:-.github/coverage-baseline.txt}"
floorfile="${3:-.github/coverage-floors.txt}"

die() {
  echo "coverage-gate: $*" >&2
  exit 2
}

[[ -f "$profile" ]] || die "profile '$profile' not found; run 'make coverage' first"
[[ -f "$baseline" ]] || die "baseline '$baseline' not found"
[[ -f "$floorfile" ]] || die "floor file '$floorfile' not found"

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

if ! awk -v a="$total" -v f="$floor" 'BEGIN { exit !(a + 0 >= f + 0) }'; then
  echo "coverage-gate: FAIL — ${total}% is below the ${floor}% floor in $baseline" >&2
  echo "Add tests for what you changed, or lower the floor deliberately in the same pull request." >&2
  exit 1
fi

module="$(go list -m | head -n 1)" || die "could not determine the module path with 'go list -m'"
[[ -n "$module" ]] || die "'go list -m' returned an empty module path"

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/coverage-gate.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT

packages="$tmpdir/packages"
go list ./internal/... > "$tmpdir/packages.full" || die "'go list ./internal/...' failed"
[[ -s "$tmpdir/packages.full" ]] || die "'go list ./internal/...' returned no packages"
# Store repo-relative package names (internal/trino), matching the profile paths
# and the floor file, not the full import path (github.com/.../internal/trino).
awk -v prefix="$module/" 'index($0, prefix) == 1 { print substr($0, length(prefix) + 1); next } { print }' \
  "$tmpdir/packages.full" > "$packages"

# Normalise the floor file into "<package>\t<percent>" lines. Comments and
# blank lines are dropped; every entry is validated so a typo cannot silently
# disable a floor.
normalized="$tmpdir/floors"
default_floor=""
: > "$normalized"
lineno=0
while IFS= read -r line || [[ -n "$line" ]]; do
  lineno=$((lineno + 1))
  line="${line%%#*}"
  fields=()
  read -r -a fields <<< "$line"
  if [[ ${#fields[@]} -eq 0 ]]; then
    continue
  fi
  if [[ ${#fields[@]} -ne 2 ]]; then
    die "$floorfile:$lineno: expected '<package> <percent>'"
  fi
  key="${fields[0]}"
  value="${fields[1]}"
  if [[ ! "$value" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
    die "$floorfile:$lineno: '$value' is not a percentage"
  fi
  if ! awk -v f="$value" 'BEGIN { exit !(f >= 0 && f <= 100) }'; then
    die "$floorfile:$lineno: '$value' is outside 0..100"
  fi
  if [[ "$key" == "default" ]]; then
    if [[ -n "$default_floor" ]]; then
      die "$floorfile:$lineno: duplicate 'default' entry"
    fi
    default_floor="$value"
    continue
  fi
  if awk -F'\t' -v k="$key" '$1 == k { found = 1 } END { exit !found }' "$normalized"; then
    die "$floorfile:$lineno: duplicate entry for '$key'"
  fi
  printf '%s\t%s\n' "$key" "$value" >> "$normalized"
done < "$floorfile"
[[ -n "$default_floor" ]] || die "$floorfile: missing 'default <percent>' entry"

# A floor for a package that no longer exists is an orphan: fail so the file
# stays in step with the tree.
while IFS=$'\t' read -r key value; do
  if ! grep -qxF "$key" "$packages"; then
    die "$floorfile names '$key', which 'go list ./internal/...' does not return"
  fi
done < "$normalized"

# Statement coverage per package, computed from the profile records rather than
# from `go tool cover -func` so the number matches `go test -cover` exactly.
coverage="$tmpdir/coverage"
awk -v module="$module" '
  NR == 1 { next }
  {
    file = $1
    sub(/:[0-9]+\.[0-9]+,.*/, "", file)
    dir = file
    sub(/\/[^\/]*$/, "", dir)
    if (index(dir, module "/") == 1) {
      pkg = substr(dir, length(module) + 2)
    } else {
      pkg = dir
    }
    statements[pkg] += $2
    if ($3 + 0 > 0) {
      covered[pkg] += $2
    }
  }
  END {
    for (pkg in statements) {
      printf "%s\t%.4f\n", pkg, 100 * covered[pkg] / statements[pkg]
    }
  }
' "$profile" > "$coverage"

failures="$tmpdir/failures"
: > "$failures"
failed=0

echo
echo "Per-package statement coverage (floors from $floorfile):"
printf '  %-46s %8s %8s %8s\n' 'PACKAGE' 'COVER' 'FLOOR' 'DELTA'
while IFS= read -r pkg; do
  pkg_floor="$(awk -F'\t' -v k="$pkg" '$1 == k { print $2 }' "$normalized")"
  if [[ -z "$pkg_floor" ]]; then
    pkg_floor="$default_floor"
  fi
  # A package that exists but writes no profile records has no tests; treat it
  # as 0% so the gate cannot be bypassed by deleting a test file.
  pkg_cov="$(awk -F'\t' -v k="$pkg" '$1 == k { print $2 }' "$coverage")"
  if [[ -z "$pkg_cov" ]]; then
    pkg_cov="0"
  fi
  verdict="$(awk -v c="$pkg_cov" -v f="$pkg_floor" 'BEGIN { printf "%d %.1f %.1f", (c + 0 >= f + 0), c, c - f }')"
  read -r ok cov delta <<< "$verdict"
  printf '  %-46s %7.1f%% %7s%% %+8.1f\n' "$pkg" "$cov" "$pkg_floor" "$delta"
  if [[ "$ok" -eq 0 ]]; then
    printf '%s\t%s\t%s\t%s\n' "$pkg" "$cov" "$pkg_floor" "$delta" >> "$failures"
    failed=$((failed + 1))
  fi
done < "$packages"

if [[ "$failed" -gt 0 ]]; then
  {
    echo
    echo "coverage-gate: FAIL — $failed package(s) below their floor:"
    while IFS=$'\t' read -r pkg cov pkg_floor delta; do
      printf '  %s: %.2f%% < %s%% (delta %.2f)\n' "$pkg" "$cov" "$pkg_floor" "$delta"
    done < "$failures"
    echo "Add tests for what you changed, or lower the floor deliberately in $floorfile in the same pull request."
  } >&2
  exit 1
fi

echo "coverage-gate: PASS"
