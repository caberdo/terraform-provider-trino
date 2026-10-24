#!/usr/bin/env bash
#
# Creates the catalogs the acceptance suite expects.
#
# The coordinator runs with catalog.management=dynamic and catalog.store=memory,
# so Trino ignores etc/catalog/*.properties and catalogs must be created over
# SQL. This script is the single source of truth for the acceptance catalogs;
# docker-compose.yml runs it in the one-shot `trino-init` service before the
# test container starts.
#
# Re-running it resets both catalogs, which keeps `docker compose run --rm test`
# idempotent without requiring `down -v` first. DROP CATALOG has no IF EXISTS
# (design spec C10), so existence is probed with SHOW CATALOGS.
set -euo pipefail

server="${TRINO_SERVER:-http://trino:8080}"
user="${TRINO_USER:-admin}"

sql() {
  trino --server "$server" --user "$user" --execute "$1"
}

# The local filesystem flag was renamed in Trino 481:
#   lib/trino-filesystem-manager/src/main/java/io/trino/filesystem/manager/FileSystemConfig.java
#   476-480: fs.native-local.enabled   481+: fs.local.enabled
# Trino rejects a catalog property it does not consume, so the name has to match
# the server version; there is no single spelling that works on both. Without
# the flag the file metastore cannot resolve file:// locations at all
# ("No factory for location"), so it cannot simply be omitted.
server_version=$(sql 'SELECT version()')
# The CLI prints string results with surrounding double quotes: "476".
server_version=${server_version//\"/}
server_version=${server_version//[[:space:]]/}
major_version=${server_version%%[!0-9]*}
if [[ -z $major_version ]]; then
  echo "could not determine the Trino version from '$server_version'" >&2
  exit 1
fi
if (( major_version < 476 )); then
  echo "Trino $server_version is below the supported floor (476)" >&2
  exit 1
fi
if (( major_version < 481 )); then
  local_filesystem_property="fs.native-local.enabled"
else
  local_filesystem_property="fs.local.enabled"
fi

# The Trino image ships neither grep nor sed, so match with bash itself.
# SHOW CATALOGS prints one double-quoted name per line.
catalog_exists() {
  local catalogs
  catalogs=$(sql 'SHOW CATALOGS')
  [[ $catalogs == *"\"$1\""* ]]
}

reset_catalog() {
  if catalog_exists "$1"; then
    sql "DROP CATALOG $1"
  fi
}

# `memory` backs schema lifecycle and property-free schema tests; it has no
# access control and no roles.
reset_catalog memory
sql 'CREATE CATALOG memory USING memory'

# `hive` backs role, role-grant and table-grant tests. The file metastore needs
# an explicit scheme in the catalog dir and the local filesystem flag to allow
# it; that flag is spelled differently before 481 (see local_filesystem_property
# above). sql-standard security auto-grants the `admin` role to the users
# admin/hive/hdfs, which the provider enables via `roles = { hive = "admin" }`
# (spec C17).
reset_catalog hive
sql "CREATE CATALOG hive USING hive WITH (
  \"hive.metastore\" = 'file',
  \"hive.metastore.catalog.dir\" = 'file:///tmp/hive',
  \"hive.security\" = 'sql-standard',
  \"$local_filesystem_property\" = 'true'
)"

sql 'SHOW CATALOGS'
