#!/usr/bin/env bash
# Validate the release metadata a release pull request carries: the version in
# `VERSION`, the changelog section that documents it and the README install
# constraint.
#
# The release pull request is the only change that moves `VERSION`, and merging
# it starts the tag workflow on `main`. This script fails the pull request when
# the three files disagree, so the mismatch is caught before the merge instead
# of after it, where it would block the release.
#
# Usage:
#   scripts/release-check.sh [VERSION] [CHANGELOG.md] [README.md]
#
# Exit codes:
#   0  the metadata is consistent
#   1  the metadata is inconsistent
#   2  a required file is missing
set -euo pipefail

version_file="${1:-VERSION}"
changelog="${2:-CHANGELOG.md}"
readme="${3:-README.md}"

fail() {
  echo "release-check: $*" >&2
  exit 1
}

for file in "$version_file" "$changelog" "$readme"; do
  [[ -f "$file" ]] || { echo "release-check: '$file' not found" >&2; exit 2; }
done

version="$(tr -d '[:space:]' < "$version_file")"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  fail "'$version_file' must hold X.Y.Z without a leading 'v', got '$version'"
fi

# The next release accumulates under Unreleased, so exactly one such heading
# must survive the cut.
unreleased="$(grep -c '^## Unreleased[[:space:]]*$' "$changelog" || true)"
if [[ "$unreleased" -ne 1 ]]; then
  fail "'$changelog' must have exactly one '## Unreleased' heading, found $unreleased"
fi

# The newest released section is the version being shipped; anything else means
# the changelog was not cut for this version.
released="$(
  awk '/^## \[[0-9]+\.[0-9]+\.[0-9]+\]/{ sub(/^## \[/, ""); sub(/\].*/, ""); print; exit }' "$changelog"
)"
if [[ -z "$released" ]]; then
  fail "'$changelog' has no '## [X.Y.Z]' release section"
fi
if [[ "$released" != "$version" ]]; then
  fail "the newest changelog section is '$released' but '$version_file' says '$version'"
fi

if ! grep -Eq "^## \[${version//./\\.}\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$" "$changelog"; then
  fail "the '## [$version]' section must be dated '- YYYY-MM-DD'"
fi

# A patch release keeps the current minor's constraint; a minor release moves
# it, which is why the expected value is derived instead of hard-coded.
minor="${version%.*}"
if ! grep -Fq "version = \"~> $minor\"" "$readme"; then
  fail "'$readme' must install with version = \"~> $minor\""
fi

echo "release-check: $version_file=$version, $changelog and $readme agree"
