#!/usr/bin/env bash
# Fail when a dependency vulnerability affects code this provider actually
# calls. Wraps `govulncheck` (pinned in .github/workflows/test.yml) and reads
# accepted findings from .github/govulncheck-accepted.txt.
#
# govulncheck already distinguishes findings the code calls from findings in
# modules that are merely required; only the former fail this gate.
#
# Suppression: if a reachable finding cannot be fixed yet (no fixed version, or
# the affected code path is unreachable in this provider's configuration), add
# an entry to the accepted file with the OSV ID and the reason:
#
#   GO-2026-1234  no fixed release; the affected xDS path is never configured
#
# Accepted entries are printed on every run so they are reconsidered, not
# forgotten. Entries that no longer match a finding produce a warning.
#
# Usage: scripts/govulncheck.sh [accepted-file]
set -euo pipefail

accepted="${1:-.github/govulncheck-accepted.txt}"
tool="${GOVULNCHECK:-govulncheck}"

die() {
  echo "govulncheck-gate: $*" >&2
  exit 2
}

command -v "$tool" >/dev/null || die "'$tool' not found; install the pinned version from .github/workflows/test.yml"
[[ -f "$accepted" ]] || die "accepted findings file '$accepted' not found"
command -v jq >/dev/null || die "'jq' not found; it is required to parse govulncheck output"

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/govulncheck.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT
report="$tmpdir/report.json"

# JSON mode exits 0 even when findings exist, so a non-zero status is a scanner
# or toolchain failure, not a vulnerability.
if ! "$tool" -format json ./... > "$report"; then
  die "'$tool -format json ./...' failed; run it directly to see the error"
fi

# Collect the IDs of findings with at least one trace entry naming a function:
# that is govulncheck's definition of "your code is affected".
jq -r '
  select(.finding)
  | select([.finding.trace[] | select(.function != null)] | length > 0)
  | [.finding.osv, (.finding.fixed_version // "no fixed version"),
     (.finding.trace[0].module // "standard library"),
     (.finding.trace[0].package // "")]
  | @tsv
' "$report" | sort -u > "$tmpdir/findings.tsv"

# Normalise the accepted file into "<OSV-ID>\t<reason>".
: > "$tmpdir/accepted.tsv"
lineno=0
while IFS= read -r line || [[ -n "$line" ]]; do
  lineno=$((lineno + 1))
  line="${line%%#*}"
  fields=()
  read -r -a fields <<< "$line"
  if [[ ${#fields[@]} -eq 0 ]]; then
    continue
  fi
  id="${fields[0]}"
  if [[ ! "$id" =~ ^GO-[0-9]{4}-[0-9]+$ ]]; then
    die "$accepted:$lineno: '$id' is not an OSV ID such as GO-2026-1234"
  fi
  if [[ ${#fields[@]} -lt 2 ]]; then
    die "$accepted:$lineno: '$id' needs a reason after it"
  fi
  reason="${line#*"$id"}"
  reason="${reason#"${reason%%[![:space:]]*}"}"
  printf '%s\t%s\n' "$id" "$reason" >> "$tmpdir/accepted.tsv"
done < "$accepted"

fail=0
while IFS=$'\t' read -r id fixed module package; do
  [[ -n "$id" ]] || continue
  reason="$(awk -F'\t' -v k="$id" '$1 == k { print $2 }' "$tmpdir/accepted.tsv")"
  if [[ -n "$reason" ]]; then
    printf 'govulncheck-gate: accepted %s (%s): %s\n' "$id" "$module" "$reason"
    continue
  fi
  if [[ "$fail" -eq 0 ]]; then
    echo "govulncheck-gate: FAIL — reachable vulnerabilities:" >&2
  fi
  fail=$((fail + 1))
  printf '  %s affects %s%s (fixed in: %s)\n' "$id" "$module" \
    "${package:+ ($package)}" "$fixed" >&2
done < "$tmpdir/findings.tsv"

# An accepted entry that no longer matches is stale, not a failure: the fix
# landed or the finding was withdrawn. Warn so it can be removed.
while IFS=$'\t' read -r id reason; do
  if ! awk -F'\t' -v k="$id" '$1 == k { found = 1 } END { exit !found }' "$tmpdir/findings.tsv"; then
    printf 'govulncheck-gate: warning: accepted %s no longer matches a finding; remove it from %s\n' "$id" "$accepted" >&2
  fi
done < "$tmpdir/accepted.tsv"

if [[ "$fail" -gt 0 ]]; then
  echo "Run 'govulncheck ./...' for the full traces, upgrade the dependency, or record an accepted finding in $accepted with a reason." >&2
  exit 1
fi

echo "govulncheck-gate: PASS (no reachable vulnerabilities)"
