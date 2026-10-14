#!/bin/bash
# PostgreSQL 首次初始化时执行（仅当数据卷为空）。
#
# 一个实例承载两个逻辑数据库：
#   - POSTGRES_DB（默认 jobpilot）  JobPilot 业务库，需要 pgvector
#   - AUTH_DB_NAME（默认 shared_auth）shared-auth 账号库，不需要 pgvector
# 合并成一个实例是为了在单机部署里省下一份数据库内存开销。
set -e

APP_DB="${POSTGRES_DB:-jobpilot}"
AUTH_DB="${AUTH_DB_NAME:-shared_auth}"

echo "初始化扩展：vector @ ${APP_DB}"
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$APP_DB" \
	-c "CREATE EXTENSION IF NOT EXISTS vector;"

if [ "$AUTH_DB" != "$APP_DB" ]; then
	echo "创建账号库：${AUTH_DB}"
	psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$APP_DB" \
		-c "CREATE DATABASE \"${AUTH_DB}\";"
fi
