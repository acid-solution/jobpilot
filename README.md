# JobPilot

JobPilot 面向准备实习、校招及初级开发岗位的求职者：从真实招聘材料整理岗位要求，结合个人经历判断能力差距，推荐适合投入的项目，并把学习安排交给 StudyFlow 执行。

当前为持续开发中的个人项目。岗位与能力判断由模型辅助生成，页面提供原文、判断依据和处理状态，结果需要核对。

## 能做什么

- 粘贴包含单个或多个岗位的招聘材料，识别候选、确认导入并分阶段分析。
- 建立当前目标的市场画像，区分完整能力、具体细项、任选要求和加分要求。
- 根据简历、经历、问答和用户确认维护个人能力画像。
- 查看知识短板，进行能力验证或复习练习。
- 比较项目设想与 GitHub 参考仓库，选择当前项目。
- 使用带业务工具、写操作确认、上下文摘要及偏好记忆的 Agent。
- 生成项目实践和知识准备两份学习计划，确认后通过 MCP 导入 StudyFlow 草稿。
- 查看模型调用、token、承担方和按配置单价计算的估算费用。

功能是否可用取决于模型凭据与服务端开关，见[开发与配置](docs/DEVELOPMENT.md)。当前生成模型接入为 DeepSeek；百炼负责可选向量化。四家生成模型并非当前支持范围。

## 快速开始

需要 Go 1.26+、Node.js 24.18+、Docker Compose，以及可用的模型配置。

1. 按[开发指南](docs/DEVELOPMENT.md#首次配置)创建 `backend/.env`，设置数据库连接和独立生成的资料加密密钥。本地后端开发可选 `AUTH_MODE=dev`；注册登录需要单独启动 shared-auth。
2. 在仓库根目录打开终端，启动数据库与 API：

~~~powershell
Set-Location backend
docker compose up -d postgres
go run ./cmd/server
~~~

3. 在仓库根目录另开终端启动页面：

~~~powershell
Set-Location frontend
npm ci
npm run dev
~~~

访问 `http://127.0.0.1:5173`；API 默认 `http://127.0.0.1:18081`。首次 API 启动会执行数据库迁移。前端登录流程及开发身份的区别见[认证接入](docs/SHARED_AUTH_SERVICE.md)。

## 技术与边界

React、TypeScript、Vite；Go、Gin、Eino；PostgreSQL、pgvector、Goose；Docker Compose、Caddy、GitHub Actions。

JobPilot 的 API 与持久化后台任务在同一服务内，shared-auth 独立负责身份。JobPilot 保存规划结果，StudyFlow 保存计划执行进度；同一账号分别登录两个网站。导入不会自动激活计划、完成任务或提升能力等级。

## 文档入口

| 需要了解什么 | 文档 |
| --- | --- |
| 完整文档目录与维护规则 | [文档导航](docs/README.md) |
| 产品范围和使用流程 | [产品说明](docs/REBUILD_PRD.md) |
| 模块、数据与系统边界 | [架构说明](ARCHITECTURE.md) |
| 本地启动、配置与排错 | [开发指南](docs/DEVELOPMENT.md) |
| 术语含义 | [业务词典](GLOSSARY.md) |
| 部署、发布和恢复 | [部署入口](deploy/README.md) |
| 测试方法和已知验证边界 | [验证指南](docs/VALIDATION.md) |

文档按 2026-10-17 的源码整理。功能覆盖不等于真实模型质量或当前线上运行状态已经验收；具体样本结论见各验证记录。
