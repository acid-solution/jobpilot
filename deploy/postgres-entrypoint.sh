#!/bin/bash
set -euo pipefail
# Reject placeholders before the upstream entrypoint initializes a data volume.
if [ -z "${POSTGRES_PASSWORD:-}" ] || [ "$POSTGRES_PASSWORD" = "__GENERATE__" ]; then
 echo "请先执行 generate-secrets.sh，不能使用未生成的数据库口令初始化数据卷" >&2
 exit 1
fi
exec /usr/local/bin/docker-entrypoint.sh "$@"
