#!/usr/bin/env bash
# Turn changelog.d/*.md fragments into a dated CHANGELOG.md section and delete
# the processed fragments. Refuses to reuse a version, to run without
# fragments or to run with a malformed fragment name.
#
# Usage: scripts/changelog-release.sh <version>
#        make changelog-release VERSION=0.6.0
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

usage() {
	echo "usage: $0 <version>" >&2
	echo "  <version> must be x.y.z, without a leading v" >&2
}

version="${1:-}"
if [[ -z "$version" ]]; then
	usage
	exit 2
fi
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
	echo "invalid version '$version': expected x.y.z" >&2
	exit 2
fi

changelog="CHANGELOG.md"
fragment_dir="changelog.d"
kinds=(breaking feature improvement bugfix security chore)

title_for() {
	case "$1" in
	breaking) echo "BREAKING CHANGES" ;;
	feature) echo "FEATURES" ;;
	improvement) echo "IMPROVEMENTS" ;;
	bugfix) echo "BUG FIXES" ;;
	security) echo "SECURITY" ;;
	chore) echo "CHORES" ;;
	esac
}

if [[ ! -f "$changelog" ]]; then
	echo "refusing: $changelog does not exist" >&2
	exit 1
fi
if ! grep -Eq '^## Unreleased[[:space:]]*$' "$changelog"; then
	echo "refusing: $changelog has no '## Unreleased' heading" >&2
	exit 1
fi
if grep -Eq "^## \[?${version//./\\.}\]?([[:space:]]|$)" "$changelog"; then
	echo "refusing: $changelog already has a section for $version" >&2
	exit 1
fi

unreleased="$(
	awk '
		/^## Unreleased[[:space:]]*$/ { inside = 1; next }
		inside && /^## / { exit }
		inside { print }
	' "$changelog"
)"
if [[ -n "${unreleased//[[:space:]]/}" ]]; then
	echo "refusing: '## Unreleased' is not empty; release entries belong in $fragment_dir/" >&2
	exit 1
fi

shopt -s nullglob
fragments=("$fragment_dir"/*.md)
shopt -u nullglob

valid=()
invalid=()
for file in "${fragments[@]}"; do
	base="${file##*/}"
	[[ "$base" == "README.md" ]] && continue

	if [[ ! "$base" =~ ^[0-9]+-(breaking|feature|improvement|bugfix|security|chore)-[a-z0-9][a-z0-9.-]*\.md$ ]]; then
		invalid+=("$file: expected <issue>-<kind>-<slug>.md with kind one of ${kinds[*]}")
		continue
	fi
	if ! grep -q '[^[:space:]]' "$file"; then
		invalid+=("$file: the fragment is empty")
		continue
	fi
	valid+=("$file")
done

if (( ${#invalid[@]} > 0 )); then
	echo "refusing: invalid changelog fragment(s):" >&2
	printf '  %s\n' "${invalid[@]}" >&2
	exit 1
fi
if (( ${#valid[@]} == 0 )); then
	echo "refusing: no changelog fragments in $fragment_dir/" >&2
	exit 1
fi

# Sort by kind and then by issue number, keeping the numeric order.
records=()
for file in "${valid[@]}"; do
	base="${file##*/}"
	issue="${base%%-*}"
	rest="${base#*-}"
	kind="${rest%%-*}"
	records+=("$kind"$'\t'"$issue"$'\t'"$file")
done
sorted="$(printf '%s\n' "${records[@]}" | sort -t$'\t' -k1,1 -k2,2n)"

section="$(mktemp)"
trap 'rm -f "$section"' EXIT

{
	printf '## [%s] - %s\n' "$version" "$(date -u +%Y-%m-%d)"
	for kind in "${kinds[@]}"; do
		files=()
		while IFS=$'\t' read -r record_kind _ file; do
			[[ "$record_kind" == "$kind" ]] && files+=("$file")
		done <<< "$sorted"
		(( ${#files[@]} > 0 )) || continue

		printf '\n%s:\n' "$(title_for "$kind")"
		for file in "${files[@]}"; do
			awk '
				{ sub(/[[:space:]]+$/, ""); lines[NR] = $0 }
				END {
					start = 1
					while (start <= NR && lines[start] == "") start++
					end = NR
					while (end >= start && lines[end] == "") end--
					for (i = start; i <= end; i++) {
						if (i == start) print "- " lines[i]
						else if (lines[i] == "") print ""
						else print "  " lines[i]
					}
				}
			' "$file"
		done
	done
	printf '\n'
} > "$section"

tmp="$(mktemp)"
trap 'rm -f "$section" "$tmp"' EXIT

awk -v section_file="$section" '
	{ lines[NR] = $0 }
	END {
		for (i = 1; i <= NR; i++) {
			print lines[i]
			if (!done && lines[i] ~ /^## Unreleased[[:space:]]*$/) {
				print ""
				while ((getline line < section_file) > 0) print line
				close(section_file)
				done = 1
				if (i < NR && lines[i + 1] == "") i++
			}
		}
		if (!done) exit 3
	}
' "$changelog" > "$tmp"

mv "$tmp" "$changelog"
trap 'rm -f "$section"' EXIT
rm -f -- "${valid[@]}"

echo "wrote $version to $changelog and removed ${#valid[@]} fragment(s)"
