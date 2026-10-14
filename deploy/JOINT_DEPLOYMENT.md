# JobPilot 与 StudyFlow 联合部署

一台 Linux 服务器运行两个网站、一个共享认证服务。JobPilot 和认证使用一个 PostgreSQL 实例中的两个数据库；StudyFlow 使用一个 MySQL 实例和 Redis。两个 Go 服务各自提供页面与 API；Caddy 统一提供 HTTPS 和转发。外部只开放 80/443，业务数据库和后端不映射公网端口。

## 1. 准备目录与域名

安装 Docker Compose，将三个仓库放在同一级目录：

```text
/srv/jobpilot-stack/
├─ jobpilot/
│  └─ deploy/
├─ shared-auth/
└─ studyflow/
```

将 `jobpilot.你的域名`、`studyflow.你的域名` 都解析到服务器。其它目录结构可修改 `SHARED_AUTH_CONTEXT` 和 `STUDYFLOW_CONTEXT`。不要再单独启动 shared-auth 或 StudyFlow 的 Compose，这份联合配置已经包含它们。

## 2. 填写配置

在 `jobpilot/deploy` 中执行：

```bash
cp .env.joint.example .env.joint
bash generate-secrets.sh .env.joint
```

编辑 `.env.joint`，至少填写：

```dotenv
PUBLIC_ORIGIN=https://jobpilot.你的域名
SITE_ADDRESS=jobpilot.你的域名
STUDYFLOW_WEB_URL=https://studyflow.你的域名
STUDYFLOW_SITE_ADDRESS=studyflow.你的域名
STUDYFLOW_MCP_URL=https://studyflow.你的域名/mcp
COOKIE_SECURE=true
RESEND_API_KEY=已有的邮件发送Key
RESEND_FROM="JobPilot <你已验证的发件地址>"
```

已有 Resend Key 和已验证的发件地址可以复用，不用重新申请。配置只写服务器文件，不提交 Git。生成器保持权限 600，并保留已有值；增加了两个独立 MySQL 密码。

联合部署默认开启学习路线导入：认证受众与 StudyFlow MCP 地址由同一个变量传入，交换凭证同时传给 JobPilot 与认证。交换请求通过 JobPilot 的 HTTPS 网站发送；浏览器则分别在两个网站正常登录，不提供自动单点登录。

JobPilot 容器必须能解析并访问两个网站的 HTTPS 域名；不能把交换地址改成 `http://auth:18082`，当前客户端不接受非回环 HTTP 地址。

平台 DeepSeek、百炼 Key 和相关功能开关按需填写。仅开启部署不代表模型相关画像任务已经就绪。

## 3. 一次启动两个项目

```bash
docker compose --env-file .env.joint -f compose.yaml -f compose.joint.yaml up -d --build
docker compose --env-file .env.joint -f compose.yaml -f compose.joint.yaml ps
```

Caddy 会为两个域名申请证书；需要 DNS 正确且服务器的 80/443 可达。启动顺序由健康检查管理：数据库与认证就绪后启动业务服务，两者就绪后启动网站入口。MySQL 首次初始化可能需要等待。

检查两个网站都能打开、验证码能收到、分别登录同一个账号后能保存资料；在 JobPilot 确认导入路线后，从回执打开 StudyFlow，核对计划并主动激活。真实邮件、证书和跨系统 HTTPS 导入需要有效凭证及实际域名，不以测试占位配置代替验收。

## 4. 更新与维护

所有命令都使用相同的 `.env.joint` 和两个 Compose 文件：

```bash
# 配置修改后重新创建受影响容器
docker compose --env-file .env.joint -f compose.yaml -f compose.joint.yaml up -d
# 更新代码后重建业务镜像
docker compose --env-file .env.joint -f compose.yaml -f compose.joint.yaml up -d --build api studyflow
# 查看业务日志
docker compose --env-file .env.joint -f compose.yaml -f compose.joint.yaml logs -f api studyflow
# 停止服务，保留数据库与密钥卷
docker compose --env-file .env.joint -f compose.yaml -f compose.joint.yaml down
```

不要对正式数据执行 `down -v`。PostgreSQL、MySQL、JWT 签名密钥和资料加密密钥应分别保存；Redis 是可丢弃缓存。更改已初始化数据库的密码需先改数据库账号密码，再更新配置，不要删除数据卷。

## 已有数据与旧部署

本方案保留 `jobpilot-stack` 项目名和原有 PostgreSQL、认证密钥卷，并新增 MySQL 数据卷。若接续原来的服务器部署，将旧配置中的数据库口令、HMAC、资料加密密钥、交换凭证与签名密钥原样保留，不能用全新随机值替代。

新的联合栈不会自动读取另一个 Compose 项目的数据库卷，也不会自动迁移本机的账号、画像或 StudyFlow 计划。若原有账号库来自独立 shared-auth 部署，需要导入账号库并保留签名密钥；StudyFlow 数据需从原 MySQL 导出导入。不要并行运行两个栈占用相同网络和网站端口。

## 配置验证记录

2026-10-14 使用独立项目名、网络和数据卷完成本机容器验收，未替换原网站：

- PostgreSQL、MySQL、Redis、认证和两业务服务健康，Caddy 正常运行。
- 两个 Host 路由的首页、健康接口返回 200。
- 两个网站的登录请求均到达共享认证，未知测试账号返回 401；开发收件箱返回 404。
- StudyFlow MCP 入口返回 401，未登录不能调用。
- Compose 检查确认仅 web 发布端口，StudyFlow 前端同源请求，MCP 受众和交换凭证一致。
- 单项目和联合部署密钥生成、权限与幂等测试通过。

入口测试使用临时 HTTP 站点和测试邮件凭证，没有发送真实邮件。正式 TLS、真实邮件及带用户授权的 HTTPS 路线导入尚需在实际服务器验收。
# HTTP 联调（不申请 TLS 证书）

HTTPS 正式部署保持默认配置。需要 HTTP 联调时，在独立环境文件中设置：

```dotenv
PUBLIC_ORIGIN=http://127.0.0.1:18080
STUDYFLOW_WEB_URL=http://127.0.0.1:18084
STUDYFLOW_MCP_URL=http://studyflow:8081/mcp
```

沿用上面的密钥生成和邮件配置步骤，启动时叠加第三份配置（Docker Compose 2.24.4 及以上）：

```bash
docker compose --env-file .env.joint -f compose.yaml -f compose.joint.yaml -f compose.joint.http-test.yaml up -d --build
```

该覆盖配置启用 `ALLOW_INSECURE_ROUTE_HTTP`、关闭 Cookie Secure，Caddy 按两个端口提供 HTTP；令牌交换和 MCP 导入通过容器内网调用。MCP 地址必须在 JobPilot、shared-auth 和 StudyFlow 三处保持一致。默认仅监听服务器本机的 18080 和 18084，通过 SSH 转发到电脑同名端口测试：

```bash
ssh -N -L 18080:127.0.0.1:18080 -L 18084:127.0.0.1:18084 user@server
```

浏览器分别打开 `http://127.0.0.1:18080` 和 `http://127.0.0.1:18084`。端口或访问地址变化时，两项浏览器 URL、转发端口和允许的 Origin 都应同步调整。两个站点共用主机时 Cookie 不按端口隔离；现有认证仍按各自客户端区分。

HTTP 开关默认关闭，只放宽服务端配置的固定路线地址协议，不放宽账号、受众、权限、摘要、幂等键和禁止重定向校验。它不影响 DeepSeek、百炼和邮件供应商原有的 HTTPS 调用。明文 HTTP 不适合真实账号、真实个人资料或模型 Key；大陆首次备案通过前不要对外开放网站。恢复正式配置时取消第三份覆盖文件，保持 `ALLOW_INSECURE_ROUTE_HTTP=false`、`COOKIE_SECURE=true` 及 HTTPS 网站地址。
