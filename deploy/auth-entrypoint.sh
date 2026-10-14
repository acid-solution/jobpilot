#!/bin/sh
set -eu

# Generated exchange credentials are also used by JobPilot. Do not configure
# a half-enabled exchange service while learning-route imports are disabled.
case "${JOBPILOT_ROUTE_EXCHANGE_ENABLED:-false}" in
  true)
    if [ -z "${JOBPILOT_EXCHANGE_SECRET:-}" ] || [ "${JOBPILOT_EXCHANGE_SECRET}" = "__GENERATE__" ] || [ -z "${STUDYFLOW_MCP_RESOURCE:-}" ]; then
      echo "学习路线导入需要生成 JOBPILOT_EXCHANGE_SECRET 并配置 STUDYFLOW_MCP_URL" >&2
      exit 1
    fi
    ;;
  false)
    unset JOBPILOT_EXCHANGE_SECRET STUDYFLOW_MCP_RESOURCE
    ;;
  *) echo "LEARNING_ROUTES_ENABLED 必须为 true 或 false" >&2; exit 1 ;;
esac

# The service validates mail credentials and never falls back to the dev inbox.
exec /app/shared-auth "$@"
