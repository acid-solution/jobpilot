#!/usr/bin/env bash
# Run in a disposable Linux container, with this directory mounted read-only
# at /source. No production files or mail providers are touched.
set -euo pipefail
if [ ! -f /.dockerenv ] || [ ! -d /source ]; then
 echo "只允许在临时 Docker 容器中运行，部署目录需只读挂载为 /source" >&2
 exit 1
fi
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
cp /source/generate-secrets.sh /source/.env.example "$work/"
bash "$work/generate-secrets.sh" >/dev/null
test "$(stat -c '%a' "$work/.env")" = 600
! grep -q '__GENERATE__' "$work/.env"
cp "$work/.env" "$work/snapshot"
bash "$work/generate-secrets.sh" >/dev/null
cmp "$work/.env" "$work/snapshot"
echo "PASS: secret generation is private and preserves existing credentials"
cp /source/.env.joint.example "$work/"
bash "$work/generate-secrets.sh" .env.joint >/dev/null
test "$(stat -c '%a' "$work/.env.joint")" = 600
! grep -q '__GENERATE__' "$work/.env.joint"
cp "$work/.env.joint" "$work/joint-snapshot"
bash "$work/generate-secrets.sh" .env.joint >/dev/null
cmp "$work/.env.joint" "$work/joint-snapshot"
cmp "$work/.env" "$work/snapshot"
echo "PASS: joint credentials are generated independently and idempotently"

mkdir -p /app
cat >/app/shared-auth <<'SH'
#!/bin/sh
case "$JOBPILOT_ROUTE_EXCHANGE_ENABLED" in
 false) test -z "${JOBPILOT_EXCHANGE_SECRET:-}"; test -z "${STUDYFLOW_MCP_RESOURCE:-}" ;;
 true) test "$JOBPILOT_EXCHANGE_SECRET" = "test-exchange-secret-at-least-32-characters"; test "$STUDYFLOW_MCP_RESOURCE" = "https://studyflow.example.test/mcp" ;;
esac
SH
chmod +x /app/shared-auth
JOBPILOT_ROUTE_EXCHANGE_ENABLED=false JOBPILOT_EXCHANGE_SECRET=unused sh /source/auth-entrypoint.sh
if JOBPILOT_ROUTE_EXCHANGE_ENABLED=true sh /source/auth-entrypoint.sh 2>/dev/null; then exit 1; fi
JOBPILOT_ROUTE_EXCHANGE_ENABLED=true JOBPILOT_EXCHANGE_SECRET=test-exchange-secret-at-least-32-characters STUDYFLOW_MCP_RESOURCE=https://studyflow.example.test/mcp sh /source/auth-entrypoint.sh
echo "PASS: exchange credentials are enabled together and fail closed"

mkdir -p "$work/bin" /etc/docker
printf '{"live-restore":true,"registry-mirrors":["https://existing.example.test"]}\n' >/etc/docker/daemon.json
cp /etc/docker/daemon.json "$work/daemon-original"
for command in apt-get git docker systemctl; do
 cat >"$work/bin/$command" <<'SH'
#!/bin/sh
if [ "$(basename "$0")" = systemctl ] && [ "${1:-}" = restart ]; then
 echo 'unexpected daemon restart' >&2; exit 1
fi
exit 0
SH
 chmod +x "$work/bin/$command"
done
PATH="$work/bin:$PATH" STACK_DIR="$work/repos" bash /source/bootstrap-server.sh >/dev/null
cmp /etc/docker/daemon.json "$work/daemon-original"
echo "PASS: existing Docker configuration and running daemon are preserved"

bash -n /source/bootstrap-server.sh /source/generate-secrets.sh /source/postgres-init/01-create-databases.sh
bash -n /source/postgres-entrypoint.sh
bash -n /source/mysql-entrypoint.sh
if MYSQL_ROOT_PASSWORD=__GENERATE__ MYSQL_PASSWORD=unused bash /source/mysql-entrypoint.sh mysqld 2>/dev/null; then exit 1; fi
if POSTGRES_PASSWORD=__GENERATE__ bash /source/postgres-entrypoint.sh postgres 2>/dev/null; then exit 1; fi
sh -n /source/auth-entrypoint.sh
echo "PASS: deployment shell syntax"

bash /source/test-release.sh
