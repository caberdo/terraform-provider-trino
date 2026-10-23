#!/usr/bin/env bash
# Summarise a Go cover profile (as produced by `go test -coverprofile=...`):
# one row per function plus the total statement coverage.
#
# Usage: scripts/coverage-summary.sh [coverage.out]
set -euo pipefail

profile="${1:-coverage.out}"

if [[ ! -f "$profile" ]]; then
  echo "coverage-summary: profile '$profile' not found" >&2
  exit 1
fi

go tool cover -func="$profile"
