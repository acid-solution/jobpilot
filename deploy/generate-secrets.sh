#!/usr/bin/env bash
# 生成部署所需的随机密钥并写入 deploy/.env。
#
# 用法（在 deploy 目录下）：
#   bash generate-secrets.sh
#
# 只会替换 .env 里仍为 __GENERATE__ 的项，已填过的值不会被覆盖，可以重复执行。
# 生成结果不会打印到终端，避免密钥进入日志或截图。
set -euo pipefail

cd "$(dirname "$0")"

if ! command -v openssl >/dev/null 2>&1; then
	echo "需要 openssl。Debian/Ubuntu 上执行：apt-get install -y openssl" >&2
	exit 1
fi

if [ ! -f .env ]; then
	cp .env.example .env
	echo "已从 .env.example 创建 .env"
fi

replace() {
	local key="$1"
	local value="$2"
	if grep -q "^${key}=__GENERATE__$" .env; then
		sed "s|^${key}=__GENERATE__$|${key}=${value}|" .env >.env.tmp
		mv .env.tmp .env
		echo "已生成 ${key}"
	else
		echo "跳过 ${key}（已有取值）"
	fi
}

# 口令用 hex：不含 / + = 等字符，可以直接拼进 DATABASE_URL 而不需要转义。
replace POSTGRES_PASSWORD "$(openssl rand -hex 24)"
# 以下三项由程序按 Base64 解析并要求解码后正好 32 字节。
replace VERIFICATION_HMAC_KEY "$(openssl rand -base64 32 | tr -d '\n')"
replace CREDENTIAL_ENCRYPTION_KEY "$(openssl rand -base64 32 | tr -d '\n')"
replace JOBPILOT_EXCHANGE_SECRET "$(openssl rand -base64 32 | tr -d '\n')"

echo
echo "完成。接下来编辑 .env，至少确认这三项："
echo "  SITE_ADDRESS   域名或 :80"
echo "  PUBLIC_ORIGIN  浏览器实际访问的地址，必须与 SITE_ADDRESS 一致"
echo "  COOKIE_SECURE  纯 HTTP 用 false，有 HTTPS 用 true"
