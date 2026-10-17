# 2026-10-14 部署配置历史验收

本页从旧部署手册保留当次记录，2026-10-17 归入验证资料。记录中的镜像打包方式、容器数量及环境只代表相应轮次；当前前端由 Go 提供、Caddy 负责转发。文档重写没有重建镜像、发送邮件或重新访问服务器。

## 单项目历史记录

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

## 联合配置历史记录

2026-10-14 使用独立项目名、网络和数据卷完成本机容器验收，未替换原网站：

- PostgreSQL、MySQL、Redis、认证和两业务服务健康，Caddy 正常运行。
- 两个 Host 路由的首页、健康接口返回 200。
- 两个网站的登录请求均到达共享认证，未知测试账号返回 401；开发收件箱返回 404。
- StudyFlow MCP 入口返回 401，未登录不能调用。
- Compose 检查确认仅 web 发布端口，StudyFlow 前端同源请求，MCP 受众和交换凭证一致。
- 单项目和联合部署密钥生成、权限与幂等测试通过。

入口测试使用临时 HTTP 站点和测试邮件凭证，没有发送真实邮件。正式 TLS、真实邮件及带用户授权的 HTTPS 路线导入尚需在实际服务器验收。

## 当前入口

[部署导航](../../deploy/README.md)、[联合部署](../../deploy/JOINT_DEPLOYMENT.md)、[发布恢复](../../deploy/CICD.md)。当前状态与业务完整性需要新的运行证据，不能仅依据这份历史记录。
