# 部署入口与单项目部署

核对日期：2026-10-17。此目录维护 JobPilot、shared-auth 及可选 StudyFlow 的单机运行配置。

| 目的 | 入口 |
| --- | --- |
| 同时运行两个网站 | [联合部署](JOINT_DEPLOYMENT.md) |
| 从 CI 固定镜像首次上线、日常发布与恢复 | [CI/CD](CICD.md) |
| 只运行 JobPilot 与认证 | 本文 |
| 开发机启动 | [开发指南](../docs/DEVELOPMENT.md) |
| 模型单价文件挂载 | [config 说明](config/README.md) |
| 历史本机配置验收 | [2026-10-14 记录](../docs/evaluations/deployment-20261014.md) |

## 运行拓扑

Caddy 对外提供网页入口、TLS 与反向代理。JobPilot 的 Go 服务提供页面与 API；shared-auth 提供身份；PostgreSQL 实例包含业务和认证两个逻辑库。只有 web 发布宿主端口，数据库与后端在内部网络通信。

当前前端构建在 JobPilot 应用镜像中，Caddy 不单独打包前端。镜像定义见 [Dockerfile](../Dockerfile)，配置见 [compose.yaml](compose.yaml)。

## 环境与目录

Linux 主机需要 Docker Engine 与 Compose。基础配置支持源码构建，shared-auth 默认位于本仓库同级：

~~~text
/srv/jobpilot-stack/
├─ jobpilot/
│  └─ deploy/
└─ shared-auth/
~~~

本机仓库目录名可不同，通过 `SHARED_AUTH_CONTEXT` 修改构建位置。项目名固定为 `jobpilot-stack`，不要同时运行占用同名项目和卷的旧版工程。

初始化服务器脚本与发布准备方式见 [CI/CD](CICD.md)。已有 Docker 配置和运行服务应先核对，不以重建环境代替迁移数据。

## 首次配置

在本目录执行：

~~~bash
test -f .env || cp .env.example .env
bash generate-secrets.sh .env
~~~

生成器只替换 `__GENERATE__`，保留已有密钥、不打印结果，配置权限为 600。接续旧数据库时必须保留资料加密密钥、数据库口令、HMAC、交换凭据和认证签名密钥。

至少填写：

| 配置 | 含义 |
| --- | --- |
| `PUBLIC_ORIGIN` | 浏览器实际访问 Origin |
| `SITE_ADDRESS` | Caddy 站点，域名触发正常 HTTPS 配置 |
| `COOKIE_SECURE` | HTTPS 为 true，与实际入口协议一致 |
| `RESEND_API_KEY`、`RESEND_FROM` | 有效服务端凭据及已验证发件地址 |
| `SHARED_AUTH_CONTEXT` | 认证源码构建位置 |
| 模型开关与平台凭据 | 依要验收功能设置，见[开发指南](../docs/DEVELOPMENT.md) |

默认生产认证使用真实邮件模式，关闭开发收件箱。开发发件器只供隔离本地环境，不等价于实际邮件投递。用户自己的模型 Key 通过设置页保存。

## 启动与维护

~~~bash
docker compose --env-file .env -f compose.yaml config --quiet
docker compose --env-file .env -f compose.yaml up -d --build --wait
docker compose --env-file .env -f compose.yaml ps
docker compose --env-file .env -f compose.yaml logs --tail 100 api auth web
~~~

修改配置后使用相同环境文件和项目重新创建受影响容器；修改前端也需要重建 api 镜像。`down` 停止服务并保留命名卷，不对业务数据执行 `down -v`。

`/health/ready` 和首页正常仅说明部分入口可用，首次启用还需登录、刷新、资料、模型及所需功能的实际验收，见[清单](../docs/REBUILD_FLOWS_AND_ACCEPTANCE.md)。

## 数据与恢复

- PostgreSQL 初始化脚本只在空卷执行，后续修改不会自动补跑。
- 数据库账号口令变更需先更新真实账号再同步配置，不能只改环境变量。
- 业务库、认证库、JWT 私钥和资料加密密钥分别留存。新生成密钥不能解密旧资料。
- 单项目源码启动不等于已经具备周期备份、异机备份与恢复演练。
- 固定镜像联合发布的备份、失败记录及兼容性检查见 [CI/CD](CICD.md)。

需要 StudyFlow 或 MCP 路线导入时使用[联合部署](JOINT_DEPLOYMENT.md)，避免叠加两套独立栈争用网络、端口与卷。
