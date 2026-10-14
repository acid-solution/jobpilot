# JobPilot 单机部署

一台 Linux 服务器上用 Docker Compose 跑起 JobPilot 的完整栈：反向代理、前端、后端 API、共享认证服务和数据库。

```
浏览器
  │  HTTP/HTTPS（只暴露 web 容器的端口）
  ▼
web（Caddy）
  ├── /                → 前端静态文件，未命中路径回退 index.html
  ├── /api/*           → api:18081   （SSE 已关闭缓冲）
  ├── /health/*        → api:18081
  ├── /auth/*          → auth:18082  （刷新令牌 Cookie 的作用域就是这个前缀）
  └── /dev/*           → 固定 404，开发收件箱接口不对公网开放

api（JobPilot 后端）──┐
auth（shared-auth）──┼── postgres（pgvector，两个库：jobpilot / shared_auth）
                     └── auth-keygen 只跑一次，生成 JWT 签名私钥
```

前端和 API 必须同源。JobPilot 后端没有任何 CORS 中间件，它只在同一个来源下工作，不要把前后端拆成两个域名。

---

## 1. 服务器准备

要求：Ubuntu 22.04/24.04 或 Debian 12，2 核 4GB 起。

**安全组只开三个端口**：22（建议限定你的出口 IP）、80、443。数据库和三个后端端口都在容器内网里，不要对公网开放。

Docker 和镜像加速可以交给 `bootstrap-server.sh`：

```bash
sudo bash bootstrap-server.sh
```

它做三件事：安装 Docker、写入 `/etc/docker/daemon.json` 配置国内镜像源、把三个仓库克隆到 `/srv/jobpilot-stack`。

> 镜像加速是必需的。阿里云 ECS 直连 Docker Hub 拉取镜像经常超时，`bootstrap-server.sh` 默认用 `docker.m.daocloud.io`。更快的做法是登录阿里云容器镜像服务控制台获取你的专属加速地址，再改 `/etc/docker/daemon.json`。

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

`generate-secrets.sh` 会生成四组随机密钥写入 `.env`（口令用 hex，可以直接拼进数据库连接串；另三项是程序要求的 Base64 32 字节）。它只替换仍是 `__GENERATE__` 的项，可以重复执行。

然后编辑 `.env`，至少确认这几项：

| 变量 | 说明 |
| --- | --- |
| `PUBLIC_ORIGIN` | 浏览器实际访问的地址，**必须逐字符匹配**，否则登录会被拒绝 |
| `SITE_ADDRESS` | Caddy 的站点地址，决定是否启用 HTTPS |
| `COOKIE_SECURE` | 有 HTTPS 填 `true`，纯 HTTP 必须 `false` |
| `PLATFORM_DEEPSEEK_API_KEY` | 平台侧 DeepSeek Key，用于分类审核和判级 |
| `EMBEDDING_ENABLED` / `PLATFORM_EMBEDDING_API_KEY` | 百炼 Embedding，不用可以保持关闭 |

启动：

```bash
docker compose up -d --build
docker compose ps
```

首次构建会拉取 Go 模块和 npm 依赖，几分钟属正常。全部 `healthy` 后打开 `PUBLIC_ORIGIN` 即可。

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

这条路免掉了备案，但拿不到证书，只能走 HTTP。两个后果要接受：

1. 浏览器地址栏会显示「不安全」。
2. `COOKIE_SECURE` 必须是 `false`。带 `Secure` 标志的 Cookie 在 HTTP 下浏览器不会保存，刷新令牌会失效，表现为登录后一刷新就掉线。

如果云厂商对未备案的 80 端口有拦截，把 `HTTP_PORT` 改成 `8080`，同时把 `PUBLIC_ORIGIN` 改成 `http://<公网IP>:8080`（端口必须一致）。

---

## 5. 邮件与注册

shared-auth 的配置校验把 `APP_ENV` 和 `EMAIL_PROVIDER` 绑在一起，只有两种可用组合：

| 组合 | 效果 |
| --- | --- |
| `AUTH_APP_ENV=staging` + `EMAIL_PROVIDER=dev` | 演示模式。验证码不投递到真实邮箱，写入内存收件箱，而该接口被 Caddy 挡在 `/dev/*` 之外。**结果是注册无法自助完成**，只能用预置账号登录。 |
| `AUTH_APP_ENV=production` + `EMAIL_PROVIDER=resend` | 真实邮件。需要自有域名配置 DKIM/SPF，并提供 Resend 凭证，同时把 `COOKIE_SECURE` 改成 `true`。 |

默认给的是第一种。它适合「自己演示 + 少量人试用」，代价是无法自助注册。

预置账号的做法：用 IP 直连或域名打开站点前，先在服务器上执行一次注册流程并读取验证码——验证码只在容器内可见：

```bash
docker compose exec auth wget -qO- "http://127.0.0.1:18082/dev/outbox?identifier=你的邮箱"
```

把拿到的 6 位码填进注册页面即可。

`EMAIL_PROVIDER=resend` 需要 `RESEND_API_KEY` 和 `RESEND_FROM`，且 `RESEND_FROM` 的域名必须已完成 Resend 侧验证。

---

## 6. 日常运维

```bash
docker compose ps                      # 状态
docker compose logs -f api             # 后端日志
docker compose logs -f web             # 访问日志
docker compose restart api             # 改完 .env 后重启对应服务
docker compose up -d --build api web   # 代码更新后重建
docker compose down                    # 停止，保留数据卷
```

**备份**：数据都在命名卷里，最需要备份的是 `jobpilot-stack_postgres-data`。

```bash
docker compose exec -T postgres pg_dump -U jobpilot jobpilot | gzip > jobpilot-$(date +%F).sql.gz
docker compose exec -T postgres pg_dump -U jobpilot shared_auth | gzip > shared_auth-$(date +%F).sql.gz
```

**`CREDENTIAL_ENCRYPTION_KEY` 必须单独留存**。用户自己填写的 DeepSeek Key 用它加密后存进数据库，密钥丢失后这些数据无法恢复，重新生成也不会让旧数据重新可读。

---

## 7. 已知约束

- **数据库口令只在首次初始化时生效**。PostgreSQL 的数据卷一旦建立，改 `POSTGRES_PASSWORD` 不会更新已有用户的口令，auth 和 api 会持续认证失败。要改口令必须先把数据导出、删除数据卷、改完再导入。
- **项目名固定为 `jobpilot-stack`**，不要改成 `jobpilot`。旧版 JobPilot 仓库的 compose 会占用同名项目和同名数据卷，同一台机器上同时部署会互相覆盖。
- `MODEL_PRICING_FILE` 留空时仍然记录 token 用量，只是费用显示为未知。需要显示费用就把 JSON 放到 `deploy/config/` 并设成 `/app/config/model-pricing.json`。
- 学习路线相关开关默认关闭。它依赖 StudyFlow 的 MCP 服务和令牌交换，三项配置齐全后才能启用。
- 首次启动时 `postgres-init/01-create-databases.sh` 会创建 `shared_auth` 库和 `vector` 扩展。**它只在数据卷为空时执行**，中途修改不会补跑。
- 后续部署 StudyFlow 时，把它的 compose 接入 `jobpilot-stack` 这个外部网络，复用同一套 shared-auth 和 PostgreSQL，不要另起一套认证服务。
- **服务器访问 github.com 可能不稳定**。`bootstrap-server.sh` 用 `git clone --depth 1` 只取最新一次提交，能减少传输量；如果仍然失败，就在本地克隆后 `scp` 上传，或把脚本里的 `GITHUB_BASE` 换成可用的镜像地址。三个仓库都是公开的，不需要任何凭证。

---

## 8. 本地验证记录

2026-10-14 在一台 Windows 机器上用 Docker 完整跑通过一次，验证项和结果：

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
