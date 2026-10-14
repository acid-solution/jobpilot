#!/usr/bin/env bash
# 生成部署所需的随机密钥并写入 deploy/.env。
#
# 用法（在 deploy 目录下）：
#   bash generate-secrets.sh
#
# 只会替换 .env 里仍为 __GENERATE__ 的项，已填过的值不会被覆盖，可以重复执行。
# 生成结果不会打印到终端，避免密钥进入日志或截图。
set -euo pipefail
umask 077

cd "$(dirname "$0")"

if ! command -v openssl >/dev/null 2>&1; then
	echo "需要 openssl。Debian/Ubuntu 上执行：apt-get install -y openssl" >&2
	exit 1
fi

if [ -L .env ]; then
	echo "拒绝写入符号链接 .env，请使用独立配置文件" >&2
	exit 1
fi
if [ ! -f .env ]; then
	cp .env.example .env
	echo "已从 .env.example 创建 .env"
fi
chmod 600 .env
temporary=""
trap 'if [ -n "$temporary" ]; then rm -f -- "$temporary"; fi' EXIT

replace() {
	local key="$1"
	local value="$2"
	local count
	count="$(grep -c "^${key}=" .env || true)"
	if [ "$count" -ne 1 ]; then
		echo "${key} 必须在 .env 中出现且只出现一次，请核对 .env.example" >&2
		exit 1
	fi
	if grep -q "^${key}=__GENERATE__$" .env; then
		temporary="$(mktemp .env.tmp.XXXXXX)"
		sed "s|^${key}=__GENERATE__$|${key}=${value}|" .env >"$temporary"
		mv -- "$temporary" .env
		temporary=""
		echo "已生成 ${key}"
	else
		echo "跳过 ${key}（已有取值）"
	fi
}

# 口令用 hex：不含 / + = 等字符，可以直接拼进 DATABASE_URL 而不需要转义。
replace POSTGRES_PASSWORD "$(openssl rand -hex 24)"
# HMAC 和资料加密密钥使用 Base64；服务交换凭证使用相同长度的随机字符串。
replace VERIFICATION_HMAC_KEY "$(openssl rand -base64 32 | tr -d '\n')"
replace CREDENTIAL_ENCRYPTION_KEY "$(openssl rand -base64 32 | tr -d '\n')"
replace JOBPILOT_EXCHANGE_SECRET "$(openssl rand -base64 32 | tr -d '\n')"

echo
echo "完成。接下来编辑 .env，至少确认以下配置："
echo "  SITE_ADDRESS   域名或 :80"
echo "  PUBLIC_ORIGIN  浏览器实际访问的地址，必须与 SITE_ADDRESS 一致"
echo "  COOKIE_SECURE  纯 HTTP 用 false，有 HTTPS 用 true"
echo "  RESEND_API_KEY 和 RESEND_FROM 真实验证码邮件所需"
