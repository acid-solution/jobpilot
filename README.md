# JobPilot（开发中）

JobPilot 帮助求职者根据真实岗位要求和自己的经历，了解能力差距，并选择适合写进简历的项目。目前仍在开发，模型生成的结果需要结合原始 JD 和个人情况核对。

## 本地启动

需要 Go 1.26+、Node.js 24.18+、PostgreSQL 17（启用 pgvector）。仓库提供了开发用的 PostgreSQL Compose 配置。正式账号登录依赖独立的 shared-auth 服务；仅本地开发时可设置 `AUTH_MODE=dev`。

1. 在 `backend` 目录运行 `docker compose up -d postgres`，或准备自己的 PostgreSQL 数据库。
2. 参考 [后端环境变量示例](backend/.env.example) 配置 `DATABASE_URL`、`CREDENTIAL_ENCRYPTION_KEY` 和认证参数。服务从进程环境变量读取配置，**不会自动加载 .env 文件**。
3. 在 `backend` 目录运行 `go run ./cmd/server`。首次启动会自动执行数据库迁移。
4. 在 `frontend` 目录运行 `npm install` 和 `npm run dev`，打开终端显示的前端地址。

默认情况下，前端把 `/api` 转发到 `127.0.0.1:18081`，把 `/auth` 转发到 `127.0.0.1:18082`。如需使用账号登录，先启动并配置 shared-auth；本地开发模式可使用 `AUTH_MODE=dev`。

如需启用全局 Agent，在设置页配置自己的 DeepSeek Key。Agent 对话使用这个 Key；写入资料前会展示变更并等待确认。原文检索和能力向量候选还需要服务端设置 `EMBEDDING_ENABLED=true`、`PLATFORM_EMBEDDING_API_KEY` 和百炼兼容模式的 `EMBEDDING_BASE_URL`。缺少平台 Embedding 配置时，原文检索不可用，其他已启用功能仍可运行。平台能力审核等可选功能及开关见环境变量示例。

## 如何使用

登录后先选择求职目标，粘贴与目标相关的岗位 JD，完成市场画像；再填写必要信息、添加经历或简历文字并评估能力，完成用户画像。之后可以生成知识短板报告、进行面试验证或复习练习，并查看项目推荐及 GitHub 参考仓库。右下角的 Agent 悬浮窗可查询当前资料、解释结果，也可在逐次确认后执行部分页面操作。

JD 分析和报告生成可能需要等待后台任务完成。目录审核、判级及部分分析功能需要平台模型配置；对应环境变量未启用时，页面可能显示等待或失败状态。

运行检查：在 `backend` 目录执行 `go test ./...`，在 `frontend` 目录执行 `npm run build`。

更多产品规则见 [需求文档](docs/REBUILD_PRD.md) 和 [页面流程](docs/REBUILD_FLOWS_AND_ACCEPTANCE.md)。
