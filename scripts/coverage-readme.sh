#!/usr/bin/env bash
# Regenerate the coverage badge and floor statement in README.md from
# .github/coverage-baseline.txt, so the published number cannot drift from the
# number `scripts/coverage-gate.sh` enforces.
#
# Usage:
#   scripts/coverage-readme.sh [README.md] [baseline]            # rewrite README.md
#   scripts/coverage-readme.sh --check [README.md] [baseline]    # exit 1 when stale
#
# Exit codes:
#   0  README.md is in sync (--check) or was rewritten
#   1  README.md is out of date (--check only)
#   2  configuration or marker error; nothing was written
set -euo pipefail

readme="README.md"
baseline=".github/coverage-baseline.txt"
check=0
positional=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --check) check=1 ;;
    -h | --help)
      echo "usage: scripts/coverage-readme.sh [--check] [README.md] [baseline]"
      exit 0
      ;;
    -*)
      echo "coverage-readme: unknown flag '$1'" >&2
      exit 2
      ;;
    *)
      case "$positional" in
        0) readme="$1" ;;
        1) baseline="$1" ;;
        *)
          echo "coverage-readme: too many arguments" >&2
          exit 2
          ;;
      esac
      positional=$((positional + 1))
      ;;
  esac
  shift
done

[[ -f "$readme" ]] || { echo "coverage-readme: '$readme' not found" >&2; exit 2; }
[[ -f "$baseline" ]] || { echo "coverage-readme: '$baseline' not found" >&2; exit 2; }

floor="$(tr -d '[:space:]' < "$baseline")"
if [[ ! "$floor" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
  echo "coverage-readme: '$baseline' is not a percentage: '$floor'" >&2
  exit 2
fi

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/coverage-readme.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT
block_file="$tmpdir/block"
out_file="$tmpdir/README.md"

{
  echo '<!-- coverage:start -->'
  printf '![Coverage floor](https://img.shields.io/badge/coverage%%20floor-%s%%25-green)\n' "$floor"
  echo
  # shellcheck disable=SC2016 # backticks are Markdown, not command substitution
  printf 'The coverage gate fails below **%s%%** (`./scripts/coverage-gate.sh`).\n' "$floor"
  echo '<!-- coverage:end -->'
} > "$block_file"

marker_count() {
  awk -v marker="$1" '$0 == marker { count++ } END { print count + 0 }' "$2"
}

for marker in '<!-- coverage:start -->' '<!-- coverage:end -->'; do
  count="$(marker_count "$marker" "$readme")"
  if [[ "$count" -ne 1 ]]; then
    echo "coverage-readme: '$readme' has $count '$marker' markers (expected exactly one)" >&2
    exit 2
  fi
done

awk -v start='<!-- coverage:start -->' -v end='<!-- coverage:end -->' -v blockfile="$block_file" '
  $0 == start {
    while ((getline line < blockfile) > 0) print line
    close(blockfile)
    inblock = 1
    next
  }
  $0 == end { inblock = 0; next }
  inblock { next }
  { print }
' "$readme" > "$out_file"

if [[ "$check" -eq 1 ]]; then
  if ! diff -u "$readme" "$out_file" >/dev/null 2>&1; then
    echo "coverage-readme: '$readme' is out of date with '$baseline'" >&2
    diff -u "$readme" "$out_file" >&2 || true
    echo "Run 'make coverage-readme' and commit the result." >&2
    exit 1
  fi
  echo "coverage-readme: '$readme' is in sync with '$baseline'"
  exit 0
fi

cat "$out_file" > "$readme"
echo "coverage-readme: updated '$readme' from '$baseline'"
