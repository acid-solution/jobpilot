# JobPilot 认证接入

核对日期：2026-10-17。本文只维护 JobPilot 接入边界。账号、验证码、刷新令牌和认证接口的实现说明由独立 shared-auth 仓库维护。

本地运行先按 [shared-auth README](https://github.com/acid-solution/shared-auth#readme)创建认证配置，再在认证仓库根目录执行 `docker compose up --build -d`。其默认 HTTP 地址为 `http://127.0.0.1:18082`；认证数据库和签名密钥由该仓库的 Compose 管理。联合部署使用统一栈，不再另外启动这套独立 Compose。

## 数据与身份

shared-auth 保存邮箱账号、密码、验证码、刷新会话及签名密钥；JobPilot 保存求职资料、模型配置与会话，只引用稳定用户 UUID。StudyFlow 使用同一身份但维护自己的业务库。

两个网站分别登录，不提供浏览器自动单点登录。JobPilot 普通访问令牌的受众为 `jobpilot`，不能直接调用要求 `studyflow` 受众的普通 API。

## 浏览器登录

浏览器通过同源 `/auth` 转发或明确的认证地址访问 shared-auth。登录取得 Access Token，刷新会话使用认证服务的 Cookie 机制；资料 API 使用 Bearer Token。注册、重置密码、注销以 shared-auth 的实际接口为准。

Vite 开发代理把认证请求转发到 18082，正式部署由 Caddy 负责。访问 Go API 原始端口不会自动得到认证反向代理。需在认证服务允许实际页面 Origin，协议、主机和端口均应一致。

## 后端验签

共享模式读取 `AUTH_JWKS_URL`、`AUTH_ISSUER`、`AUTH_AUDIENCE`，通过 JWKS 验证签名与令牌身份。默认 issuer 为 `shared-auth`，audience 为 `jobpilot`。实现见 [identity](../backend/internal/identity)。

`AUTH_MODE=dev` 只在 `APP_ENV=development` 可用，提供固定开发用户；它不是生产登录方式，也不让前端自动绕过账号页面。

## 学习路线代办

JobPilot 在用户确认固定路线内容后，用源访问令牌和服务凭据向 `/auth/v1/token/exchange` 换取短期代办令牌。

代办令牌绑定目标 MCP 资源、导入键、内容摘要及导入/回执权限，不进入模型、前端或持久化对话。StudyFlow 普通页面仍需正常登录。

服务端配置需一致：

| JobPilot | 对应要求 |
| --- | --- |
| `AUTH_EXCHANGE_URL` | shared-auth 令牌交换入口 |
| `JOBPILOT_EXCHANGE_SECRET` | 与 shared-auth 的同名服务凭据一致 |
| `STUDYFLOW_MCP_URL` | 与认证及 StudyFlow 的 `STUDYFLOW_MCP_RESOURCE` 完全相同 |
| `STUDYFLOW_WEB_URL` | 用户正常登录、查看草稿的网站地址 |

生产地址使用 HTTPS；开发回环 HTTP 与显式联调覆盖配置的区别见[联合部署](../deploy/JOINT_DEPLOYMENT.md)。权限校验不会因联调开关而放宽。

## 核验

用两个账号检查资料隔离；用错误受众检查 401；用不同来源目标和内容摘要验证代办拒绝；导入后确认两个网站仍各自登录。完整命令与数据库测试入口见[验证指南](VALIDATION.md)。
