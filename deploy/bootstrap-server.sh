#!/usr/bin/env bash
# 在一台全新的 Ubuntu / Debian 服务器上准备 JobPilot 部署环境。
#
#   sudo bash bootstrap-server.sh
#
# 做三件事：
#   1. 安装 Docker（已安装则跳过）
#   2. 配置国内镜像加速（已配置则跳过）
#   3. 创建 /srv/jobpilot-stack 并克隆需要的仓库
#
# 不会启动任何服务，也不会改动防火墙。安全组请在云控制台自行放行 22/80/443。
set -euo pipefail

STACK_DIR="${STACK_DIR:-/srv/jobpilot-stack}"
REGISTRY_MIRROR="${REGISTRY_MIRROR:-docker.m.daocloud.io}"
GITHUB_BASE="${GITHUB_BASE:-https://github.com/acid-solution}"

if [ "$(id -u)" -ne 0 ]; then
	echo "请用 root 运行：sudo bash bootstrap-server.sh" >&2
	exit 1
fi

if ! command -v apt-get >/dev/null 2>&1; then
	echo "这个脚本只支持 Debian / Ubuntu。其他发行版请手动安装 Docker。" >&2
	exit 1
fi

echo "==> 1/3 安装 Docker"
apt-get update
apt-get install -y ca-certificates curl git openssl
installed_docker=false
if command -v docker >/dev/null 2>&1; then
	echo "Docker 已安装：$(docker --version)"
else
	# 官方安装脚本的阿里云镜像。脚本内容会执行到系统上，执行前建议先自行确认。
	curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
	sh /tmp/get-docker.sh --mirror Aliyun
	installed_docker=true
fi
if ! docker compose version >/dev/null 2>&1; then
	echo "缺少 Docker Compose 插件，请安装后重新运行" >&2
	exit 1
fi

echo "==> 2/3 配置镜像加速"
mkdir -p /etc/docker
if [ -f /etc/docker/daemon.json ]; then
	echo "保留现有 daemon.json；镜像加速及日志配置不作覆盖"
else
	if [[ ! "$REGISTRY_MIRROR" =~ ^[a-zA-Z0-9.:-]+$ ]]; then
		echo "REGISTRY_MIRROR 应为镜像源主机名，可带端口，不含协议或路径" >&2
		exit 1
	fi
	cat >/etc/docker/daemon.json <<EOF
{
  "registry-mirrors": ["https://${REGISTRY_MIRROR}"],
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "20m",
    "max-file": "5"
  }
}
EOF
	chmod 644 /etc/docker/daemon.json
	echo "已创建 Docker 配置；现有 Docker 如需立即加载，请自行选择维护时间重启"
fi
systemctl enable docker
if [ "$installed_docker" = true ]; then
	systemctl restart docker
elif ! systemctl is-active --quiet docker; then
	systemctl start docker
fi
echo "提示：登录阿里云容器镜像服务控制台可以拿到专属加速地址，换成它通常更快。"

echo "==> 3/3 准备代码目录 ${STACK_DIR}"
mkdir -p "${STACK_DIR}"
cd "${STACK_DIR}"
for repo in shared-auth jobpilot studyflow; do
	if [ -d "${repo}/.git" ]; then
		echo "已存在，跳过：${repo}"
		continue
	fi
	echo "克隆 ${repo}"
	if ! git clone --depth 1 "${GITHUB_BASE}/${repo}.git"; then
		echo "克隆 ${repo} 失败。" >&2
		echo "如果服务器访问 github.com 不稳定，可以在本地克隆后用 scp 上传，或改 GITHUB_BASE 指向可用的镜像。" >&2
		exit 1
	fi
done

cat <<EOF

准备完成。下一步：

  cd ${STACK_DIR}/jobpilot/deploy
  cp .env.example .env
  bash generate-secrets.sh
  # 编辑 .env：填好域名、RESEND_API_KEY、RESEND_FROM 和所需模型 Key
  docker compose up -d --build

详细说明见 ${STACK_DIR}/jobpilot/deploy/README.md
EOF
