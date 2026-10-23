#!/usr/bin/env bash
#
# Read-only verification of the main-protection ruleset.
#
# Exits 0 when the ruleset matches the policy, 1 on drift, and 3 when the
# rulesets API cannot be read (the normal case for a fork or a token without
# administration permission). Set BRANCH_PROTECTION_SOFT=1 to make that third
# case a warning instead of an error; protection-drift.yml does that.
#
# Usage:
#   ./scripts/check-branch-protection.sh
#   GITHUB_REPOSITORY=owner/repo ./scripts/check-branch-protection.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib/workflow-jobs.sh"

RULESET_NAME="${RULESET_NAME:-main-protection}"
FALLBACK_FILE="$WORKFLOW_JOBS_ROOT/.github/branch-protection-fallback"
SOFT="${BRANCH_PROTECTION_SOFT:-0}"

DEFAULT_REQUIRED_CONTEXTS=("Lint & Security" "Test")
if [ -n "${REQUIRED_CONTEXTS:-}" ]; then
  IFS=',' read -r -a REQUIRED_CONTEXTS_ARR <<< "$REQUIRED_CONTEXTS"
else
  REQUIRED_CONTEXTS_ARR=("${DEFAULT_REQUIRED_CONTEXTS[@]}")
fi

for i in "${!REQUIRED_CONTEXTS_ARR[@]}"; do
  value="${REQUIRED_CONTEXTS_ARR[i]}"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  REQUIRED_CONTEXTS_ARR[i]="$value"
done

REPO="${GITHUB_REPOSITORY:-}"
if [ -z "$REPO" ]; then
  REPO="$(gh repo view --json nameWithOwner --jq .nameWithOwner 2>/dev/null)" \
    || { printf 'error: cannot determine the repository; set GITHUB_REPOSITORY=owner/repo\n' >&2; exit 3; }
fi

failures=()
fail() {
  failures+=("$*")
}

unreadable() {
  if [ "$SOFT" = "1" ]; then
    printf 'warning: cannot read rulesets for %s; skipping the drift check\n' "$REPO" >&2
    exit 3
  fi
  printf 'error: cannot read rulesets for %s; the token needs administration read permission\n' "$REPO" >&2
  exit 3
}

if ! validate_required_contexts "${REQUIRED_CONTEXTS_ARR[@]}"; then
  printf '\nThe required contexts in this script are not valid check contexts:\n' >&2
  printf '  %s\n' "${REQUIRED_CONTEXTS_ARR[@]}" >&2
  printf 'pull_request job names in the tree:\n' >&2
  all_pr_jobs | cut -f1 | sort -u | sed 's/^/  - /' >&2
  exit 1
fi

if ! ruleset_id="$(gh api --paginate "repos/$REPO/rulesets" \
  --jq ".[] | select(.name == \"$RULESET_NAME\") | .id" 2>/dev/null)"; then
  unreadable
fi

ruleset_id="${ruleset_id%%$'\n'*}"
if [ -z "$ruleset_id" ]; then
  printf 'branch protection drift detected for %s:\n' "$REPO" >&2
  printf '  - ruleset %s does not exist\n' "$RULESET_NAME" >&2
  exit 1
fi

if ! detail="$(gh api "repos/$REPO/rulesets/$ruleset_id" 2>/dev/null)"; then
  unreadable
fi

enforcement="$(printf '%s' "$detail" | jq -r '.enforcement // ""')"
[ "$enforcement" = "active" ] || fail "ruleset enforcement is '$enforcement', expected 'active'"

has_rule() {
  printf '%s' "$detail" | jq -e --arg type "$1" '[.rules[] | select(.type == $type)] | length > 0' >/dev/null
}

for rule in deletion non_fast_forward required_linear_history pull_request required_status_checks; do
  has_rule "$rule" || fail "ruleset is missing the '$rule' rule"
done

includes_default="$(printf '%s' "$detail" \
  | jq -r '[.conditions.ref_name.include[]?] | index("~DEFAULT_BRANCH") != null')"
[ "$includes_default" = "true" ] || fail "ruleset conditions do not include '~DEFAULT_BRANCH'"

bypass_count="$(printf '%s' "$detail" | jq -r '.bypass_actors | length')"
[ "$bypass_count" = "0" ] || fail "ruleset has $bypass_count bypass actor(s); a solo maintainer must not bypass the gate"

thread_resolution="$(printf '%s' "$detail" \
  | jq -r '.rules[] | select(.type == "pull_request") | .parameters.required_review_thread_resolution // false')"
[ "$thread_resolution" = "true" ] || fail "required_review_thread_resolution is not enabled"

actual_contexts="$(printf '%s' "$detail" \
  | jq -r '.rules[] | select(.type == "required_status_checks") | .parameters.required_status_checks[]?.context' \
  | sort)"
expected_contexts="$(printf '%s\n' "${REQUIRED_CONTEXTS_ARR[@]}" | sort)"
if [ "$actual_contexts" != "$expected_contexts" ]; then
  fail "required contexts are [$(printf '%s' "$actual_contexts" | tr '\n' ' ' | sed 's/ $//')], expected [$(printf '%s' "$expected_contexts" | tr '\n' ' ' | sed 's/ $//')]"
fi

if [ -f "$FALLBACK_FILE" ]; then
  printf 'note: %s exists; accepting the documented no-merge-queue fallback\n' "${FALLBACK_FILE#"$WORKFLOW_JOBS_ROOT"/}" >&2
else
  has_rule merge_queue || fail "ruleset is missing the 'merge_queue' rule (or document the fallback in .github/branch-protection-fallback)"

  strict="$(printf '%s' "$detail" \
    | jq -r '.rules[] | select(.type == "required_status_checks") | .parameters.strict_required_status_checks_policy // false')"
  [ "$strict" = "true" ] || fail "strict_required_status_checks_policy is not enabled"
fi

# A required context that no longer resolves to a pull_request job, or that now
# lives on a conditional/matrix job, can never report and would block forever.
while IFS= read -r context; do
  [ -n "$context" ] || continue
  matches="$(all_pr_jobs | awk -F'\t' -v wanted="$context" '$1 == wanted { count++ } END { print count + 0 }')"

  if [ "$matches" -eq 0 ]; then
    fail "required context '$context' is not a job name in a workflow that runs on pull_request"
    continue
  fi
  if [ "$matches" -gt 1 ]; then
    fail "required context '$context' matches $matches jobs; context names must be unique"
    continue
  fi

  line="$(all_pr_jobs | awk -F'\t' -v wanted="$context" '$1 == wanted')"
  has_if="$(printf '%s\n' "$line" | cut -f4)"
  has_matrix="$(printf '%s\n' "$line" | cut -f5)"
  [ "$has_if" = "0" ] || fail "required context '$context' belongs to a job with an 'if:' condition"
  [ "$has_matrix" = "0" ] || fail "required context '$context' belongs to a matrix job"
done <<< "$actual_contexts"

if [ "${#failures[@]}" -gt 0 ]; then
  printf 'branch protection drift detected for %s:\n' "$REPO" >&2
  printf '  - %s\n' "${failures[@]}" >&2
  exit 1
fi

printf 'branch protection for %s is correct (%s is active).\n' "$REPO" "$RULESET_NAME"
