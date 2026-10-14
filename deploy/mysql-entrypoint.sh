#!/bin/bash
set -euo pipefail
for name in MYSQL_ROOT_PASSWORD MYSQL_PASSWORD; do
 if [ -z "${!name:-}" ] || [ "${!name}" = "__GENERATE__" ]; then
  echo "请先运行 generate-secrets.sh .env.joint，不能用未生成的 MySQL 密码初始化数据卷" >&2
  exit 1
 fi
done
exec /usr/local/bin/docker-entrypoint.sh "$@"
