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
# the flag the file metastore cannot resolve local:// locations at all
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
# the local filesystem flag to resolve locations; that flag is spelled
# differently before 481 (see local_filesystem_property above). sql-standard
# security auto-grants the `admin` role to the users admin/hive/hdfs, which the
# provider enables via `roles = { hive = "admin" }` (spec C17).
#
# The catalog dir and the local filesystem root are both load-bearing:
#   * hive.metastore.catalog.dir must use the local:// scheme. The filesystem
#     iterator returns local:///... locations and FileHiveMetastore.getAllDatabases
#     strips the catalog-dir string prefix off each one; with a file:/// prefix
#     the strip leaves a leading path segment and every schema is skipped, so
#     SHOW SCHEMAS only ever returns information_schema.
#   * local.location defaults to java.io.tmpdir (/tmp) and is prepended to the
#     path part of every location, so file:///tmp/hive would be written to the
#     physical path /tmp/tmp/hive. Rooting it at / keeps the logical path and
#     the physical path identical (/tmp/hive).
reset_catalog hive
sql "CREATE CATALOG hive USING hive WITH (
  \"hive.metastore\" = 'file',
  \"hive.metastore.catalog.dir\" = 'local:///tmp/hive',
  \"hive.security\" = 'sql-standard',
  \"local.location\" = '/',
  \"$local_filesystem_property\" = 'true'
)"

# `iceberg` backs materialized-view acceptance. Trino supports CREATE
# MATERIALIZED VIEW only on Iceberg catalogs backed by a Hive metastore: the
# REST catalog fails with "createMaterializedView is not supported for Iceberg
# REST catalog". It therefore uses the `hive-metastore` service, not the file
# metastore behind `hive`, and writes its warehouse into the named volume
# mounted at /warehouse. Data and metadata paths stay valid across the
# coordinator recreated by `docker compose run --rm test`; tables that outlive
# a run are dropped by each test's own fixture.
reset_catalog iceberg
sql "CREATE CATALOG iceberg USING iceberg WITH (
  \"hive.metastore.uri\" = 'thrift://hive-metastore:9083',
  \"$local_filesystem_property\" = 'true'
)"

sql 'SHOW CATALOGS'
