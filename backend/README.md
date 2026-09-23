# JobPilot 后端

当前已完成一条从用户操作到市场画像的可运行链路：从岗位目录选择当前求职目标，粘贴完整 JD，将原文和分析任务在同一 PostgreSQL 事务中持久化，由 Worker 完成有效性判断、岗位分类和能力归一化，最后生成当前目标的市场能力画像。

## 本地运行

先在相邻的 `shared-auth` 目录启动认证服务，再启动启用 pgvector 的 JobPilot PostgreSQL：

```powershell
Set-Location ..\..\shared-auth
docker compose up --build -d
Set-Location ..\jobpilot-next\backend
docker compose up -d postgres
```

再设置环境变量并启动 API：

```powershell
$env:APP_ENV='development'
$env:HTTP_ADDR='127.0.0.1:18081'
$env:DATABASE_URL='postgres://jobpilot:jobpilot@127.0.0.1:5433/jobpilot?sslmode=disable'
$env:AUTH_MODE='shared'
$env:AUTH_JWKS_URL='http://127.0.0.1:18082/.well-known/jwks.json'
$env:AUTH_ISSUER='shared-auth'
$env:AUTH_AUDIENCE='jobpilot'
$keyBytes = [byte[]]::new(32)
[Security.Cryptography.RandomNumberGenerator]::Fill($keyBytes)
$env:CREDENTIAL_ENCRYPTION_KEY=[Convert]::ToBase64String($keyBytes)
$env:ABILITY_REVIEW_ENABLED='true'
$env:ABILITY_REVIEW_USER_DAILY_LIMIT='3'
$env:ABILITY_REVIEW_GLOBAL_DAILY_LIMIT='30'
$env:PLATFORM_DEEPSEEK_API_KEY='平台专用 DeepSeek Key'
$env:PLATFORM_REVIEW_MODEL='deepseek-chat'
go run ./cmd/server
```

服务启动时自动执行 Goose 迁移。默认通过 shared-auth 的 JWKS 本地验证 RS256 Access Token，并校验 `iss=shared-auth`、`aud=jobpilot`、有效期、用户 UUID 和会话 UUID。开发环境如需临时绕过认证，可显式设置 `AUTH_MODE=dev`，此时才会使用 `DEV_USER_ID` 或请求头 `X-Dev-User-ID`。

## 当前接口

| 方法 | 地址 | 用途 |
|---|---|---|
| `GET` | `/health/live` | 进程存活检查 |
| `GET` | `/health/ready` | 数据库就绪检查 |
| `GET` | `/api/v1/targets/current` | 读取当前求职目标 |
| `PUT` | `/api/v1/targets/current` | 创建或编辑当前求职目标 |
| `GET` | `/api/v1/catalog/job-directions` | 读取系统岗位大类与小类目录 |
| `POST` | `/api/v1/jds` | 保存完整 JD，并创建持久化分析任务 |
| `POST` | `/api/v1/jds/batch` | 批量保存最多 50 份 JD，逐项返回新增、重复或无效结果 |
| `GET` | `/api/v1/jds` | 列出当前目标下的 JD；支持 `status`、`q`、`company`、`ability`、`category` 筛选 |
| `GET` | `/api/v1/jds/:id` | 查看一份 JD 与任务状态 |
| `PUT` | `/api/v1/jds/:id` | 修改 JD 原文，清除旧结果并重新分析 |
| `DELETE` | `/api/v1/jds/:id` | 删除错误 JD 及其分析数据 |
| `POST` | `/api/v1/jds/:id/retry` | 重新提交失败的 JD 分析任务 |
| `POST` | `/api/v1/jds/:id/ability-reviews/retry` | 重新提交技术失败的能力目录审核 |
| `GET/PUT/DELETE` | `/api/v1/model-configs/deepseek` | 读取、保存或删除 DeepSeek 配置 |
| `POST` | `/api/v1/model-configs/deepseek/test` | 测试输入或已保存的 DeepSeek Key |
| `GET` | `/api/v1/market-profile` | 读取当前目标的有效 JD 数量和市场能力画像 |

`POST /api/v1/jds` 只负责基础文本检查和可靠接收资料，成功时返回 HTTP 202。服务内 Worker 随后使用用户加密保存的 DeepSeek Key，在一次调用中完成语义有效性判断、内容解析、目录岗位分类和能力归一化。每个岗位小类都向模型提供定义、正向信号、排除信号和易混淆分类规则；模型必须依据职责原文选择恰好一个主导小类，可以补充多个次要小类，并为每项分类返回逐字证据和独立理由。能力要求按 `single`、`any_of`、`at_least_n` 匿名组保存，每个候选项分别归一化并保留具体限定和原文证据。后端会再次校验这些约束；只有有效且与当前目标匹配的 JD 才计入市场画像。达到 10 份有效 JD 后，市场画像视为完成，但仍允许继续添加 JD。

市场能力覆盖按不同 JD 去重。`single` 能力和 `any_of` 的每个候选都可以独立覆盖对应要求；`at_least_n` 的单个候选只是组合的一部分，不计为单项即可满足。统计只表达该能力能够覆盖多少份 JD，与用户是否掌握该能力无关。

目录外能力不会直接进入公开能力列表。系统先按标准名和别名精确匹配；仍无法匹配时创建去重审核申请，由平台 DeepSeek Key 驱动的独立 Worker 返回 `reuse_existing`、`approve_new` 或 `reject`。新增能力会在一个事务内写入定义、别名和 L0-L5 六档说明并关联所有等待选项；拒绝结果会清理候选和空要求组。审核 Worker 使用独立租约、心跳、过期恢复、每日账号/平台额度和平台调用用量记录。账号和全平台的每日额度分别由 `ABILITY_REVIEW_USER_DAILY_LIMIT`、`ABILITY_REVIEW_GLOBAL_DAILY_LIMIT` 控制，设为 `0` 表示不限次数。平台 Key 缺失时请求保持配置阻塞，补齐环境变量并重启后自动恢复。

当前求职目标的方向必须从数据库岗位目录选择。迁移会尝试把旧的自由文本方向按名称匹配到目录；无法可靠匹配的目标会标记为需要重新选择，并阻止继续提交 JD，避免错误分类名称进入后续画像。

任务领取使用租约凭证和心跳。Worker 崩溃后，过期任务会在运行期扫描中重新排队；旧 Worker 即使稍后返回，也不能覆盖新领取者写入的结果。

当前接入模型固定为 `deepseek-flash`，使用 DeepSeek Chat Completions 的非思考模式和 JSON Output。API Key 使用 AES-256-GCM 加密，密钥由 `CREDENTIAL_ENCRYPTION_KEY` 环境变量提供；该变量必须是 32 个随机字节的 Base64 文本，不能提交到仓库。

## 验证

```powershell
go test ./...
```
