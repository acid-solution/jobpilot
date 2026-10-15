# JobPilot（开发中）

服务器上同时运行 JobPilot 和 StudyFlow，见 [联合部署说明](deploy/JOINT_DEPLOYMENT.md)。

JobPilot 帮助求职者根据真实岗位要求和自己的经历，了解能力差距，并选择适合写进简历的项目。目前仍在开发，模型生成的结果需要结合原始 JD 和个人情况核对。

## 本地启动

需要 Go 1.26+、Node.js 24.18+、PostgreSQL 17（启用 pgvector）。仓库提供了开发用的 PostgreSQL Compose 配置。正式账号登录依赖独立的 shared-auth 服务；仅本地开发时可设置 `AUTH_MODE=dev`。

1. 在 `backend` 目录运行 `docker compose up -d postgres`，或准备自己的 PostgreSQL 数据库。
2. 将 [后端配置示例](backend/.env.example) 复制为 `backend/.env`，配置 `DATABASE_URL`、`CREDENTIAL_ENCRYPTION_KEY` 和认证参数。后端自动读取启动目录中的 `.env`，同名进程环境变量优先；线上也可以只使用环境变量。
3. 在 `backend` 目录运行 `go run ./cmd/server`。首次启动会自动执行数据库迁移。
4. 在 `frontend` 目录运行 `npm install` 和 `npm run dev`，打开终端显示的前端地址。

默认情况下，前端把 `/api` 转发到 `127.0.0.1:18081`，把 `/auth` 转发到 `127.0.0.1:18082`。如需使用账号登录，先启动并配置 shared-auth；本地开发模式可使用 `AUTH_MODE=dev`。

每类已启用后台任务默认运行 2 个 Worker。在 `backend/.env` 中设置 `WORKER_COUNT=2` 可调整并发数量，只接受正整数，修改后需重启后端。此配置按任务类型生效，例如 JD 分析和能力判级各运行 2 个 Worker；不会自动开启被关闭的功能。

如需启用全局 Agent，在设置页配置自己的 DeepSeek Key。Agent 对话使用这个 Key；写入资料前会展示变更并等待确认。原文检索和能力向量候选还需要服务端设置 `EMBEDDING_ENABLED=true`、`PLATFORM_EMBEDDING_API_KEY` 和百炼兼容模式的 `EMBEDDING_BASE_URL`。缺少平台 Embedding 配置时，原文检索不可用，其他已启用功能仍可运行。平台能力审核等可选功能及开关见环境变量示例。

## 如何使用

登录后先选择求职目标，粘贴与目标相关的岗位 JD，完成市场画像；再填写必要信息、添加经历或简历文字并评估能力，完成用户画像。之后可以生成知识短板报告、进行面试验证或复习练习，并查看项目推荐及 GitHub 参考仓库。右下角的 Agent 悬浮窗可查询当前资料、解释结果，也可在逐次确认后执行部分页面操作。

JD 分析和报告生成可能需要等待后台任务完成。目录审核、判级及部分分析功能需要平台模型配置；对应环境变量未启用时，页面可能显示等待或失败状态。

运行检查：在 `backend` 目录执行 `go test ./...`，在 `frontend` 目录执行 `npm run build`。

## 合并部署

正式部署时，统一镜像包含 Go 服务和构建后的前端文件，页面与 API 都由 Go 提供；Caddy 负责 HTTPS 和认证转发。在 `deploy` 目录配置 `.env` 后执行 `docker compose up -d --build`，具体步骤见 [部署说明](deploy/README.md)。

本机也可以先在 `frontend` 运行 `npm run build`，再在 `backend` 将 `FRONTEND_DIST_DIR` 设置为 `../frontend/dist` 并启动后端，通过 `http://127.0.0.1:18081` 查看页面。共享账号登录还需要部署说明中的同源 Caddy 认证入口；直接访问 Go 端口不会转发 `/auth`。日常开发仍推荐 Vite，`FRONTEND_DIST_DIR` 留空即可。

更多产品规则见 [需求文档](docs/REBUILD_PRD.md) 和 [页面流程](docs/REBUILD_FLOWS_AND_ACCEPTANCE.md)。

## 模型用量与费用

在“设置 → 模型用量”查看最近 30 天的调用、输入输出 token、缓存命中和估算费用。可以按日期、功能和费用承担方筛选，展开一次操作查看各次调用。用户 Key 与关联平台 Key 分开统计，共享平台费用不能跨用户相加。

费用按服务端配置单价估算，不代表供应商实际扣款。配置 `MODEL_PRICING_FILE` 指向带版本号的价格文件，修改后重启后端。未配置或模型无法准确匹配时显示费用未知；历史缓存和价格无法恢复，历史费用保持未知。配置格式和统计边界见 [模型用量说明](docs/MODEL_USAGE.md)。

## 学习路线与 StudyFlow

选择项目后，从侧边导航打开独立的“学习路线”页面，分别设置项目实践和知识准备的投入预算，生成并编辑预览。两份计划可以按课时推进或按日期安排到天。确认导入后，打开 StudyFlow，用同一账号正常登录，再主动激活计划草稿。更新知识计划会保留项目计划和已有执行进度。

启用此功能需要先部署支持路线导入的 StudyFlow 和支持令牌交换的 shared-auth。在后端配置 `LEARNING_ROUTES_ENABLED=true`、`AUTH_EXCHANGE_URL`、`JOBPILOT_EXCHANGE_SECRET`、`STUDYFLOW_MCP_URL` 和 `STUDYFLOW_WEB_URL`；shared-auth 使用相同服务密钥，两个服务中的 `STUDYFLOW_MCP_RESOURCE` 必须与 MCP 地址一致。具体部署配置见 [学习路线使用说明](docs/LEARNING_ROUTES.md)。

## 助手记忆

在右下角助手的“记忆”入口查看、添加、编辑或遗忘偏好。助手也会提出记忆建议，确认后才保存；建议不会阻塞聊天。项目和学习偏好默认属于当前求职目标，回答详略适用于全部目标。

已保存偏好会用于之后主动生成的推荐和路线；本次调整意见可以临时覆盖，不会修改长期偏好。修改偏好不会更换已有项目、让旧推荐过期或改写现有路线。

遗忘保留可查看的聊天，但对应范围遗忘前的对话和摘要不再送给模型，未确认旧操作需要重新提出。删除会话不会删除独立保存的长期偏好。长对话会按需整理摘要，额外 DeepSeek 调用使用用户 Key，并计入“模型用量”。详见 [上下文与记忆说明](docs/AGENT_CONTEXT_MEMORY.md)。
