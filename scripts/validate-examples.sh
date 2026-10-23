#!/usr/bin/env bash
# Validate every Terraform example under examples/ against the provider built
# from this working tree.
#
# Usage: scripts/validate-examples.sh
#   TRINO_PLUGIN_DIR=<dir>  override the plugin directory (for parallel runs)
#   REQUIRE_TERRAFORM=1     fail instead of skipping when terraform is absent
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

if ! command -v terraform >/dev/null 2>&1; then
  if [ "${REQUIRE_TERRAFORM:-0}" = "1" ]; then
    echo "terraform is required but not installed (REQUIRE_TERRAFORM=1)" >&2
    exit 1
  fi
  echo "terraform is not installed; skipping example validation" >&2
  exit 0
fi

terraform fmt -check -recursive examples

VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
OS="$(go env GOOS)"
ARCH="$(go env GOARCH)"
PLUGIN_DIR="${TRINO_PLUGIN_DIR:-${TMPDIR:-/tmp}/trino-provider-dev}"
BIN_DIR="${PLUGIN_DIR}/registry.terraform.io/caberdo/trino/${VERSION}/${OS}_${ARCH}"
CLI_CONFIG="${PLUGIN_DIR}/dev.tfrc"

mkdir -p "$BIN_DIR"
echo "building provider ${VERSION}..."
go build -o "${BIN_DIR}/terraform-provider-trino" \
  -ldflags "-X main.version=${VERSION}" .

# Examples are validated without registry access: dev_overrides points both the
# canonical source (caberdo/trino) and Terraform's inferred default
# (hashicorp/trino) at the binary built above.
printf 'provider_installation {\n  dev_overrides {\n    "caberdo/trino" = "%s"\n    "hashicorp/trino" = "%s"\n  }\n  direct {}\n}\n' \
  "$BIN_DIR" "$BIN_DIR" >"$CLI_CONFIG"

export TF_CLI_CONFIG_FILE="$CLI_CONFIG"
export TF_IN_AUTOMATION=1

# A module is a directory with at least one .tf file. Every example directory is
# a standalone module in v1, so each is validated in place. `terraform validate`
# does not need `terraform init` when dev_overrides supplies the provider; that
# also keeps the check independent of the Terraform Registry.
while IFS= read -r dir; do
  echo "==> ${dir#./}"
  (
    cd "$dir"
    terraform validate -no-color
  )
done < <(find examples -name '*.tf' -exec dirname {} \; | sort -u)

echo "all examples validated"
