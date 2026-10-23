#!/usr/bin/env bash
#
# Install or update the main-protection repository ruleset.
#
# This is idempotent: it looks the ruleset up by name and PUTs the same payload
# on every run. It refuses to guess context names: the requested contexts are
# validated against the job `name:` values in workflows that run on
# pull_request. Run it with a token that has admin on the repository.
#
# Usage:
#   ./scripts/setup-branch-protection.sh
#   GITHUB_REPOSITORY=owner/repo ./scripts/setup-branch-protection.sh
#
# The one maintainer today cannot approve their own PR, so the ruleset asks for
# zero approvals and no code-owner review. Raise both in the same change that
# adds a second maintainer (see CONTRIBUTING.md).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib/workflow-jobs.sh"

RULESET_NAME="${RULESET_NAME:-main-protection}"
FALLBACK_FILE="$WORKFLOW_JOBS_ROOT/.github/branch-protection-fallback"

DEFAULT_REQUIRED_CONTEXTS=("Lint & Security" "Test")
if [ -n "${REQUIRED_CONTEXTS:-}" ]; then
  IFS=',' read -r -a REQUIRED_CONTEXTS_ARR <<< "$REQUIRED_CONTEXTS"
else
  REQUIRED_CONTEXTS_ARR=("${DEFAULT_REQUIRED_CONTEXTS[@]}")
fi

# Trim surrounding whitespace so "Lint & Security, Test" is also accepted.
for i in "${!REQUIRED_CONTEXTS_ARR[@]}"; do
  value="${REQUIRED_CONTEXTS_ARR[i]}"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  REQUIRED_CONTEXTS_ARR[i]="$value"
done

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

REPO="${GITHUB_REPOSITORY:-}"
if [ -z "$REPO" ]; then
  REPO="$(gh repo view --json nameWithOwner --jq .nameWithOwner 2>/dev/null)" \
    || die "cannot determine the repository; set GITHUB_REPOSITORY=owner/repo"
fi

if ! all_pr_jobs | grep -q .; then
  die "no workflows under .github/workflows run on pull_request"
fi

if ! validate_required_contexts "${REQUIRED_CONTEXTS_ARR[@]}"; then
  printf '\npull_request job names available to require:\n' >&2
  all_pr_jobs | cut -f1 | sort -u | sed 's/^/  - /' >&2
  exit 1
fi

admin="$(gh api "repos/$REPO" --jq '.permissions.admin' 2>/dev/null || printf 'false')"
[ "$admin" = "true" ] || die "the current token does not have admin permission on $REPO"

contexts_json="$(printf '%s\n' "${REQUIRED_CONTEXTS_ARR[@]}" | jq -R . | jq -s 'map({context: .})')"

fallback="0"
if [ -f "$FALLBACK_FILE" ]; then
  fallback="1"
  printf 'warning: %s exists; installing the documented no-merge-queue fallback\n' "${FALLBACK_FILE#"$WORKFLOW_JOBS_ROOT"/}" >&2
fi

strict="true"
[ "$fallback" = "0" ] || strict="false"

payload="$(jq -n \
  --arg name "$RULESET_NAME" \
  --argjson contexts "$contexts_json" \
  --argjson strict "$strict" \
  '{
    name: $name,
    target: "branch",
    enforcement: "active",
    bypass_actors: [],
    conditions: {
      ref_name: {include: ["~DEFAULT_BRANCH"], exclude: []}
    },
    rules: [
      {type: "deletion"},
      {type: "non_fast_forward"},
      {type: "required_linear_history"},
      {
        type: "pull_request",
        parameters: {
          required_approving_review_count: 0,
          dismiss_stale_reviews_on_push: true,
          require_code_owner_review: false,
          require_last_push_approval: false,
          required_review_thread_resolution: true
        }
      },
      {
        type: "required_status_checks",
        parameters: {
          strict_required_status_checks_policy: $strict,
          required_status_checks: $contexts
        }
      }
    ]
  }')"

if [ "$fallback" = "0" ]; then
  payload="$(printf '%s' "$payload" | jq '.rules += [{
    type: "merge_queue",
    parameters: {
      merge_method: "SQUASH",
      grouping_strategy: "ALLGREEN",
      max_entries_to_build: 3,
      min_entries_to_merge: 1,
      max_entries_to_merge: 1,
      min_entries_to_merge_wait_minutes: 5,
      check_response_timeout_minutes: 60
    }
  }]')"
fi

if ! ruleset_id="$(gh api --paginate "repos/$REPO/rulesets" \
  --jq ".[] | select(.name == \"$RULESET_NAME\") | .id" 2>/dev/null)"; then
  die "cannot list rulesets for $REPO (is the token allowed to administer the repository?)"
fi
ruleset_id="${ruleset_id%%$'\n'*}"

apply_ruleset() {
  if [ -n "$ruleset_id" ]; then
    gh api --method PUT "repos/$REPO/rulesets/$ruleset_id" --input - <<< "$payload"
  else
    gh api --method POST "repos/$REPO/rulesets" --input - <<< "$payload"
  fi
}

if ! output="$(apply_ruleset 2>&1)"; then
  printf 'error: GitHub rejected the ruleset for %s:\n%s\n' "$REPO" "$output" >&2
  if [ "$fallback" = "0" ]; then
    printf '\nIf this repository cannot use a merge queue, write the trade-off down in\n' >&2
    printf '%s and re-run. The check script then expects strict:false.\n' "${FALLBACK_FILE#"$WORKFLOW_JOBS_ROOT"/}" >&2
  fi
  exit 1
fi

action="created"
[ -z "$ruleset_id" ] || action="updated"
printf '%s ruleset %s on %s with contexts: %s\n' "$action" "$RULESET_NAME" "$REPO" "$(printf '%s, ' "${REQUIRED_CONTEXTS_ARR[@]}" | sed 's/, $//')"
