# JobPilot 开发指南

所有命令从当前仓库根目录开始；运行中的服务使用独立终端。依赖版本以 [go.mod](../backend/go.mod)、[package.json](../frontend/package.json)、锁文件及容器配置为准。

## 环境

- Go 1.26+，Node.js 24.18+，npm，Docker Compose。
- PostgreSQL 17 与 pgvector；开发配置使用 `pgvector/pgvector:0.8.6-pg17`。
- 注册登录需要独立 shared-auth；模型功能需要自己的 DeepSeek Key。
- 端口：前端 5173、API 18081、开发数据库 5433、认证 18082。

## 首次配置

在 `backend` 中创建本地配置，保留已有文件：

~~~powershell
if (-not (Test-Path -LiteralPath backend/.env)) {
    Copy-Item -LiteralPath backend/.env.example -Destination backend/.env
}
~~~

编辑 `backend/.env`：

| 配置 | 要求 |
| --- | --- |
| `DATABASE_URL` | 对应开发 Compose 的数据库；示例只用于本地 |
| `CREDENTIAL_ENCRYPTION_KEY` | Base64 编码的独立 32 字节密钥 |
| `APP_ENV`、`AUTH_MODE` | 默认开发环境、共享认证；`dev` 身份只允许开发环境 |
| `AUTH_JWKS_URL`、`AUTH_ISSUER`、`AUTH_AUDIENCE` | 默认指向本地 shared-auth，受众为 `jobpilot` |
| `WORKER_COUNT` | 默认每类任务 2 个消费者，必须为正整数 |
| `FRONTEND_DIST_DIR` | Vite 开发留空，合并服务指向构建产物 |

本机生成加密密钥：

~~~powershell
node -e "process.stdout.write(require('node:crypto').randomBytes(32).toString('base64'))"
~~~

将结果只填入自己的忽略配置。已有加密数据继续使用原密钥；随机更换密钥不会迁移旧凭据。

配置从启动目录的 `.env` 读取，再由进程环境覆盖，显式空环境变量也优先。完整配置名称见[配置示例](../backend/.env.example)和[读取代码](../backend/internal/config/config.go)。

## 数据库、API 和前端

终端一：

~~~powershell
Set-Location backend
docker compose up -d postgres
go run ./cmd/server
~~~

终端二：

~~~powershell
Set-Location frontend
npm ci
npm run dev
~~~

API 启动自动执行[嵌入 Goose 迁移](../backend/internal/storage/postgres/migrations/sql)。`/health/live` 检查进程，`/health/ready` 检查数据库连通，不检查所有模型凭据。

Vite 将 `/api`、`/health` 转发到 18081，将 `/auth`、`/dev` 转发到 18082。使用 `http://127.0.0.1:5173` 打开页面。

## 身份与生成配置

共享认证需要按 [shared-auth 接入](SHARED_AUTH_SERVICE.md)先启动认证服务，并允许当前前端 Origin。`AUTH_MODE=dev` 只为后端开发提供固定用户身份，不会让浏览器自动跳过登录；前端完整验证使用共享账号。

登录后在设置页保存并测试 DeepSeek 配置。用户 Key 经服务端加密保存，不写进前端构建环境。`DEEPSEEK_BASE_URL` 是服务端目的地址，不是页面中的任意代理地址设置。

个人材料支持文字型 PDF、DOCX、UTF-8 TXT 和 Markdown，文件最多 10 MB；提取文字需在 20–100,000 字符范围内。扫描图片 PDF 不提供 OCR。原文件在内存中提取后不持久化，用户核对文字再确认用于分析；见[提取实现](../backend/internal/profile/document.go)。

| 功能 | 服务端开关或配置 | 缺失时的影响 |
| --- | --- | --- |
| 全局 Agent | `AGENT_ENABLED`，默认 true | 关闭时不注册相应助手服务 |
| 能力目录审核 | `ABILITY_REVIEW_ENABLED` | 待审核候选不能自动成为公共能力 |
| JD 能力判级 | `JD_ABILITY_GRADING_ENABLED` | 相关等级可能未就绪 |
| 新岗位分类审核 | `JOB_CLASSIFICATION_REVIEW_ENABLED` | 新分类不能靠普通解析自动批准 |
| 平台模型 | `PLATFORM_DEEPSEEK_API_KEY`、`PLATFORM_REVIEW_MODEL` | 平台审核和判级无法完成 |
| 向量服务 | `EMBEDDING_ENABLED`、`EMBEDDING_BASE_URL`、`PLATFORM_EMBEDDING_API_KEY` | 不启用向量增强；原文向量检索不可用 |
| GitHub | `GITHUB_TOKEN` | 仍受匿名额度和外部网络限制 |
| 学习路线 | `LEARNING_ROUTES_ENABLED` | 默认 false；规划与导入见[专项配置](LEARNING_ROUTES.md) |
| 用量价格 | `MODEL_PRICING_FILE` | 仍记录可信 token，费用显示未知 |

平台审核与判级默认关闭，向量默认关闭。开发者需按实际要验收的链路选择开启，并核对服务端凭据，不能把等待状态当成分析已完成。

## 构建与测试

~~~powershell
Set-Location frontend
npm test
npm run typecheck
npm run build
~~~

后端命令、隔离数据库及可选浏览器验证见[验证指南](VALIDATION.md)。合并服务在构建前端后，从 `backend` 设置 `FRONTEND_DIST_DIR=../frontend/dist` 并启动；直接访问 API 端口不会代理 `/auth`，完整同源入口使用[部署配置](../deploy/README.md)。

## 常见问题

| 现象 | 优先检查 |
| --- | --- |
| 启动提示缺配置 | 启动目录、环境覆盖、Base64 密钥是否确为 32 字节 |
| 登录后请求 401 | 认证服务、JWT 受众、JWKS 地址、时钟及当前 Origin |
| 材料识别失败 | 先看导入会话错误，再看用户模型 Key；不要把失败当作无效材料 |
| 采集完成但分析仍阻塞 | 未评估能力、目录审核、判级、个人必要信息 |
| 页面结果未更新 | 活动轮询、当前目标、旧请求保护及后端失败状态 |
| 导入待核实 | 用同一幂等键查询或重试，勿另建一份内容相同的新导入 |
| 费用未知 | 返回模型名称是否精确匹配价格文件，供应商是否提供可信用量 |

上下文预算见[记忆配置](AGENT_CONTEXT_MEMORY.md)，部署环境使用[联合部署说明](../deploy/JOINT_DEPLOYMENT.md)。
