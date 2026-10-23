#!/usr/bin/env bash
#
# Healthcheck for the acceptance coordinator.
#
# The coordinator answers /v1/info with `starting:true` while it boots and only
# flips the flag once it can accept statements, so wait for that instead of for
# the first HTTP response. The Trino image ships neither grep nor sed, hence the
# bash substring test.
set -euo pipefail

info=$(curl -fsS http://localhost:8080/v1/info)
[[ $info == *'"starting":false'* ]]
