# JobPilot 单机部署

一台 Linux 服务器上用 Docker Compose 跑起 JobPilot：Go 同时提供前端页面与 API，Caddy 提供 HTTPS 与转发，共享认证和数据库独立运行。

```
浏览器
  │  HTTP/HTTPS（只暴露 web 容器的端口）
  ▼
web（Caddy）
  ├── 页面与静态资源    → api:18081（Go 提供文件与页面回退）
  ├── /api/*           → api:18081   （SSE 已关闭缓冲）
  ├── /health/*        → api:18081
  ├── /auth/*          → auth:18082  （刷新令牌 Cookie 的作用域就是这个前缀）
  └── /dev/*           → 固定 404，开发收件箱接口不对公网开放

api（Go + 前端产物）──┐
auth（shared-auth）──┼── postgres（pgvector，两个库：jobpilot / shared_auth）
                     └── auth-keygen 只跑一次，生成 JWT 签名私钥
```

前端和 API 必须同源。JobPilot 后端没有任何 CORS 中间件，它只在同一个来源下工作，不要把前后端拆成两个域名。

仓库根目录 Dockerfile 分别构建 Node 前端和 Go 后端，再把 `dist` 与服务程序放入同一运行镜像。web 服务使用 Caddy 官方镜像，不保存前端文件。`FRONTEND_DIST_DIR=/app/frontend/dist` 由 Compose 设置；目录或首页缺失时 API 拒绝启动。

---

## 1. 服务器准备

要求：Ubuntu 22.04/24.04 或 Debian 12，2 核 4GB 起。

**安全组只开三个端口**：22（建议限定你的出口 IP）、80、443。数据库和三个后端端口都在容器内网里，不要对公网开放。

Docker 和镜像加速可以交给 `bootstrap-server.sh`：

```bash
sudo bash bootstrap-server.sh
```

它安装所需工具、准备 Docker 配置，并把三个仓库克隆到 `/srv/jobpilot-stack`。已有 `daemon.json` 会保留，已有运行中的 Docker 不会被自动重启；如需加载新配置，请在维护时间手动重启。

> 如果 Docker Hub 拉取失败，可以配置可用的镜像加速地址。脚本仅在没有现有 Docker 配置时创建默认加速配置；不保证任一公共镜像源始终可用。

---

## 2. 目录规划

compose 默认按「三个仓库并列克隆」的相对路径查找 shared-auth：

```
/srv/jobpilot-stack/
├── jobpilot/       ← 本仓库（公开仓库 acid-solution/jobpilot）
├── shared-auth/    ← 必须一起部署，否则登录不可用（公开仓库 acid-solution/shared-auth）
└── studyflow/      ← 后续部署 StudyFlow 时再克隆
```

如果放在别的位置，在 `.env` 里改 `SHARED_AUTH_CONTEXT` 指向 shared-auth 的绝对路径。

---

## 3. 首次部署

```bash
cd /srv/jobpilot-stack/jobpilot/deploy
cp .env.example .env
bash generate-secrets.sh
```

`generate-secrets.sh` 生成数据库口令、验证码 HMAC、资料加密密钥及服务交换凭证，设置 `.env` 权限为 `600`，不打印密钥。它只替换 `__GENERATE__`，保留已有值；不要用新密钥覆盖已有数据库的加密密钥。

然后编辑 `.env`，至少确认这几项：

| 变量 | 说明 |
| --- | --- |
| `PUBLIC_ORIGIN` | 浏览器实际访问的地址，**必须逐字符匹配**，否则登录会被拒绝 |
| `SITE_ADDRESS` | Caddy 的站点地址，决定是否启用 HTTPS |
| `COOKIE_SECURE` | 有 HTTPS 填 `true`，纯 HTTP 必须 `false` |
| `RESEND_API_KEY` | 必填，Resend 服务端邮件发送 Key |
| `RESEND_FROM` | 必填，已通过 Resend 域名验证的发件地址，例如 `JobPilot <verify@your-domain.com>` |
| `PLATFORM_DEEPSEEK_API_KEY` | 平台侧 DeepSeek Key，用于分类审核和判级 |
| `EMBEDDING_ENABLED` / `PLATFORM_EMBEDDING_API_KEY` | 百炼 Embedding，不用可以保持关闭 |

启动：

```bash
docker compose up -d --build
docker compose ps
```

首次构建会拉取 Go 模块和 npm 依赖。postgres、auth、api 应为 `healthy`，auth-keygen 应以状态 0 退出，web 应为运行中；再打开 `PUBLIC_ORIGIN`。缺少邮件凭证时 auth 会报错退出，不会退回开发邮件模式。

---

## 4. 两种部署形态

### 有域名（推荐）

```
SITE_ADDRESS=jobpilot.example.com
PUBLIC_ORIGIN=https://jobpilot.example.com
COOKIE_SECURE=true
HTTP_PORT=80
HTTPS_PORT=443
```

Caddy 会自动申请 Let's Encrypt 证书，80 端口自动跳转到 443。域名需要提前解析到服务器 IP。

如果域名指向大陆地域的 ECS，**必须先完成 ICP 备案**，否则 80/443 会被拦截。

### 只有 IP，不用域名

```
SITE_ADDRESS=:80
PUBLIC_ORIGIN=http://<公网IP>
COOKIE_SECURE=false
HTTP_PORT=80
```

上述配置使用纯 HTTP，需要同步将 `COOKIE_SECURE` 改为 `false`。它与默认的域名 HTTPS 部署不同：

1. 浏览器地址栏会显示「不安全」。
2. `COOKIE_SECURE` 必须是 `false`。带 `Secure` 标志的 Cookie 在 HTTP 下浏览器不会保存，刷新令牌会失效，表现为登录后一刷新就掉线。

如使用非标准端口，把 `HTTP_PORT` 改成对应端口，同时更新 `PUBLIC_ORIGIN`（例如 `http://<公网IP>:8080`）。云厂商的端口与备案要求请按实际部署地域核对，修改端口不保证能绕过限制。

---

## 5. 邮件与注册

默认使用真实邮件验证码：

```dotenv
AUTH_APP_ENV=production
EMAIL_PROVIDER=resend
DEV_OUTBOX_ENABLED=false
COOKIE_SECURE=true
RESEND_API_KEY=你的服务端邮件发送Key
RESEND_FROM="JobPilot <verify@your-domain.com>"
```

先在 Resend 验证自己的发件域名，填写它提供的 DNS 记录，再设置 Key 与发件地址。服务不会自动申请 Key，也不会把验证码打印到公网日志。仅填写示例地址不能保证真实投递。

如果明确需要本地演示，可手动设置 `AUTH_APP_ENV=staging`、`EMAIL_PROVIDER=dev`、`DEV_OUTBOX_ENABLED=true`；此时不发送真实邮件，`/dev/*` 仍不会对公网开放。先请求验证码，再从容器内读取：

```bash
docker compose exec auth wget -qO- "http://127.0.0.1:18082/dev/outbox?identifier=你的邮箱"
```

拿到验证码后填入注册页面。开发收件箱不适合公开站点自助注册。

`EMAIL_PROVIDER=resend` 需要 `RESEND_API_KEY` 和 `RESEND_FROM`，且 `RESEND_FROM` 的域名必须已完成 Resend 侧验证。

---

## 6. 日常运维

```bash
docker compose ps                      # 状态
docker compose logs -f api             # 后端日志
docker compose logs -f web             # 访问日志
docker compose up -d                  # 改完 .env：重建受影响容器，加载新环境变量
docker compose up -d --build api web   # 代码更新后重建
docker compose down                    # 停止，保留数据卷
```

**备份**：数据都在命名卷里，最需要备份的是 `jobpilot-stack_postgres-data`。

```bash
docker compose exec -T postgres pg_dump -U jobpilot jobpilot | gzip > jobpilot-$(date +%F).sql.gz
docker compose exec -T postgres pg_dump -U jobpilot shared_auth | gzip > shared_auth-$(date +%F).sql.gz
```

**`CREDENTIAL_ENCRYPTION_KEY` 必须单独留存**。用户自己填写的 DeepSeek Key 用它加密后存进数据库，密钥丢失后这些数据无法恢复，重新生成也不会让旧数据重新可读。

从旧的前后端分别打包方式更新时，直接执行 `docker compose up -d --build`。项目名和数据卷名称保持不变，数据库及认证签名密钥会继续使用；不要执行 `down -v`。前端更新需要重建 api 镜像，不再重建独立前端镜像。

---

## 7. 已知约束

- **数据库口令只在首次初始化时生效**。已有数据卷不能只改 `.env`。先通过 `docker compose exec postgres psql -U jobpilot -d postgres` 进入数据库，用 `\password jobpilot` 修改角色密码，再同步 `.env` 的 `POSTGRES_PASSWORD`，运行 `docker compose up -d`。自定义用户名时替换 `jobpilot`。不需要删除数据卷，已有数据和资料加密密钥保持不变。
- 数据库连接串使用配置口令，生成器默认生成 hex 口令以避免 URL 保留字符；手动更换时使用同类口令，不直接填入带 `@`、`:`、`/` 等字符的值。
- **项目名固定为 `jobpilot-stack`**，不要改成 `jobpilot`。旧版 JobPilot 仓库的 compose 会占用同名项目和同名数据卷，同一台机器上同时部署会互相覆盖。
- `MODEL_PRICING_FILE` 留空时仍然记录 token 用量，只是费用显示为未知。需要显示费用就把 JSON 放到 `deploy/config/` 并设成 `/app/config/model-pricing.json`。
- 学习路线默认关闭。开启时配置 `LEARNING_ROUTES_ENABLED=true`、`STUDYFLOW_MCP_URL` 和 `STUDYFLOW_WEB_URL`。生成的 `JOBPILOT_EXCHANGE_SECRET` 会同时传给 API 和 auth；`AUTH_EXCHANGE_URL` 留空默认使用内网认证地址。`STUDYFLOW_MCP_URL` 同时传为 auth 的令牌受众，必须与 StudyFlow 的 `STUDYFLOW_MCP_RESOURCE` 一致，正式部署使用 HTTPS。关闭学习路线时不启用交换服务；开启但配置缺失时明确报错。
- 首次启动时 `postgres-init/01-create-databases.sh` 会创建 `shared_auth` 库和 `vector` 扩展。**它只在数据卷为空时执行**，中途修改不会补跑。
- 后续部署 StudyFlow 时可接入 `jobpilot-stack` 网络并复用 shared-auth。StudyFlow 业务库目前使用 MySQL，不能直接复用 JobPilot 的 PostgreSQL 业务库；其数据库、Redis 和 MCP 仍需按 StudyFlow 的部署文档配置。
- **服务器访问 github.com 可能不稳定**。`bootstrap-server.sh` 用 `git clone --depth 1` 只取最新一次提交，能减少传输量；如果仍然失败，就在本地克隆后 `scp` 上传，或把脚本里的 `GITHUB_BASE` 换成可用的镜像地址。三个仓库都是公开的，不需要任何凭证。

---

## 8. 本地验证记录

2026-10-14 初版曾使用演示邮件模式验证完整栈，下表为该次记录。当前默认改为真实邮件；新增部署脚本隔离测试覆盖密钥权限和幂等生成、保留已有 Docker 配置、认证交换开关与数据库口令占位符保护。真实邮件投递仍需填入有效 Resend 凭证后验证，不以测试凭证的启动成功代替投递验收。

| 验证项 | 结果 |
| --- | --- |
| 后端镜像构建（`CGO_ENABLED=0`，Alpine 运行层） | 通过 |
| 前端镜像构建（Node 构建 → Caddy 运行层） | 通过 |
| 镜像内是否混入 `backend/.env.local` 的本地密钥 | 未混入，`/app` 下只有二进制和空 config 目录 |
| 五个容器全部 healthy | 通过 |
| 首页、静态资源、SPA 回退 | 200 / 200 / 200 |
| `/health/ready` 经反向代理到数据库 | `{"status":"ok"}` |
| `/dev/outbox` 对公网是否可达 | 404，已挡住 |
| 注册 → 登录 → 带令牌调 JobPilot API | 通过（API 返回 `409 target_required`，说明已通过 JWKS 鉴权并进入业务逻辑） |
| 不带令牌调 JobPilot API | 401 |
| 刷新 Cookie 的路径与 HttpOnly | `Path=/auth`、`HttpOnly`，与 Caddy 路由一致 |
| 用刷新 Cookie 换取新令牌 | 通过 |
| 安全响应头 | `X-Content-Type-Options`、`X-Frame-Options`、`Referrer-Policy` 均已下发，`Server` 已隐藏 |

验证时用的是国内镜像源（`docker.m.daocloud.io`、`goproxy.cn`、`registry.npmmirror.com`），所以 Dockerfile 里的默认值就是按国内网络环境设的。

部署脚本可以在临时、无网络容器中测试（在 `deploy` 目录运行）：

```bash
docker run --rm --network none -v "$PWD:/source:ro" \
  --entrypoint bash pgvector/pgvector:0.8.6-pg17 /source/test-deployment.sh
```

本次修正使用独立测试项目和测试邮件凭证验证了 production/resend 模式启动：数据库、认证与 API 健康，首页和 `/health/ready` 正常，公网开发收件箱返回 404。测试没有发送真实邮件，没有替换本机原有网站。

前后端合并部署另外验证了 Go 提供首页、页面刷新、静态资源与缓存，错误 API 和缺失资源返回 404；Caddy 镜像没有前端文件。隔离栈临时使用开发收件箱完成登录、刷新 Cookie、带令牌 API 与无令牌 401 测试，正式默认仍是 Resend。使用模拟上游和生产 Caddy 配置验证了 SSE 首个小事件在请求完成前到达客户端，无需调用真实模型。
