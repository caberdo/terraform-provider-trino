#!/usr/bin/env bash
#
# Read the job names out of GitHub Actions workflow files.
#
# The required status checks in the main-protection ruleset are matched against
# a job's `name:`, not its key under `jobs:`. This helper parses the small,
# stable subset of workflow YAML the repository actually uses, so the setup and
# drift-check scripts never have to guess a context name. It deliberately does
# not try to be a general YAML parser.
#
# Sourced by scripts/setup-branch-protection.sh and
# scripts/check-branch-protection.sh.

set -euo pipefail

WORKFLOW_JOBS_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# Print "#PR<TAB>0|1" followed by "name<TAB>key<TAB>has_if<TAB>has_matrix" for
# every job in the workflow. A job without an explicit `name:` reports its key,
# which is what GitHub uses as the check context.
_workflow_parse() {
  local file="$1"

  awk '
    BEGIN { in_on = 0; in_jobs = 0; cur = ""; count = 0; pr = 0 }

    { sub(/\r$/, "") }
    /^[[:space:]]*#/ { next }

    /^on:[[:space:]]*$/ { in_on = 1; in_jobs = 0; next }
    /^on:.*pull_request/ { pr = 1 }
    /^on:[[:space:]]*\[/ { in_on = 0; next }

    /^[^[:space:]#]/ {
      if ($0 ~ /^jobs:[[:space:]]*$/) { in_jobs = 1; in_on = 0; next }
      in_on = 0
      in_jobs = 0
      next
    }

    in_on && /^  pull_request:/ { pr = 1; next }

    in_jobs && /^  [A-Za-z0-9_.-]+:[[:space:]]*$/ {
      cur = $0
      sub(/:.*/, "", cur)
      sub(/^  /, "", cur)
      count++
      order[count] = cur
      name[cur] = ""
      jobif[cur] = 0
      matrix[cur] = 0
      next
    }

    in_jobs && cur != "" && /^    name:/ {
      value = $0
      sub(/^    name:[[:space:]]*/, "", value)
      gsub(/^["\047]|["\047]$/, "", value)
      name[cur] = value
      next
    }

    in_jobs && cur != "" && /^    if:/ { jobif[cur] = 1; next }
    in_jobs && cur != "" && /^    strategy:/ { matrix[cur] = 1; next }

    END {
      print "#PR\t" pr
      for (i = 1; i <= count; i++) {
        key = order[i]
        value = name[key]
        if (value == "") value = key
        print value "\t" key "\t" jobif[key] "\t" matrix[key]
      }
    }
  ' "$file"
}

# Print 1 when the workflow runs on pull_request, otherwise 0.
workflow_pr_trigger() {
  _workflow_parse "$1" | awk -F'\t' '$1 == "#PR" { print $2; found = 1 } END { if (!found) print 0 }'
}

# Print this workflow's jobs as "name<TAB>key<TAB>has_if<TAB>has_matrix".
workflow_jobs() {
  _workflow_parse "$1" | awk -F'\t' '$1 != "#PR"'
}

# Print every job from a pull_request workflow as
# "name<TAB>workflow-file<TAB>key<TAB>has_if<TAB>has_matrix".
all_pr_jobs() {
  local file trigger

  for file in "$WORKFLOW_JOBS_ROOT"/.github/workflows/*.yml; do
    [ -e "$file" ] || continue
    trigger="$(workflow_pr_trigger "$file")"
    [ "$trigger" = "1" ] || continue
    workflow_jobs "$file" | awk -F'\t' -v source="$(basename "$file")" \
      '{ printf "%s\t%s\t%s\t%s\t%s\n", $1, source, $2, $3, $4 }'
  done
}

# Fail (non-zero, message on stderr) when any supplied context is not a real,
# unconditional, non-matrix job name in a workflow that runs on pull_request.
validate_required_contexts() {
  local context matches line has_if has_matrix status=0

  for context in "$@"; do
    matches="$(all_pr_jobs | awk -F'\t' -v wanted="$context" '$1 == wanted { count++ } END { print count + 0 }')"

    if [ "$matches" -eq 0 ]; then
      printf "required context '%s' is not a job name in a workflow that runs on pull_request\n" "$context" >&2
      status=1
      continue
    fi

    if [ "$matches" -gt 1 ]; then
      printf "required context '%s' matches %s jobs; context names must be unique across workflows\n" "$context" "$matches" >&2
      status=1
      continue
    fi

    line="$(all_pr_jobs | awk -F'\t' -v wanted="$context" '$1 == wanted')"
    has_if="$(printf '%s\n' "$line" | cut -f4)"
    has_matrix="$(printf '%s\n' "$line" | cut -f5)"

    if [ "$has_if" != "0" ]; then
      printf "required context '%s' belongs to a job with an 'if:' condition; it can be skipped and block PRs forever\n" "$context" >&2
      status=1
    fi

    if [ "$has_matrix" != "0" ]; then
      printf "required context '%s' belongs to a matrix job; no single context is reported\n" "$context" >&2
      status=1
    fi
  done

  return "$status"
}
