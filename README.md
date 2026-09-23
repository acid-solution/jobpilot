# JobPilot 新版

本目录是本轮 JobPilot 重开发的项目根目录。后续需求完善、设计、代码实现和部署配置统一在这里维护。

前端原型页面已经基本完成。后端目前完成 shared-auth 接入、目录化求职目标、JD 原文入库、可恢复分析任务、DeepSeek 有效性与内容解析、岗位分类、能力归一化、单份 JD 能力判级和市场能力画像。注册、登录、密码重置、会话刷新和退出已经连接相邻的 `shared-auth` 服务；市场画像页和 DeepSeek 设置页已经改用真实接口。

## 运行前端

需要 Node.js 24.18.0 或更高版本。在 `frontend/` 目录执行：

```bash
npm install
npm run dev
```

前端开发服务器会把 `/api` 代理到 `http://127.0.0.1:18081`，把 `/auth` 代理到 `http://127.0.0.1:18082`。请先启动相邻的 `shared-auth`，再按[后端说明](backend/README.md)启动 JobPilot。工作台、用户画像、项目推荐、知识短板和 Agent 对话仍包含原型数据；账号会话、目录化求职目标、JD 新增/编辑/删除/批量导入/重复检测/筛选搜索、处理状态、分类与归一化结果、市场能力画像，以及设置页的 DeepSeek Key 配置，已经连接各自的 PostgreSQL。

## 文档入口

| 文档 | 内容 |
|---|---|
| [需求文档](docs/REBUILD_PRD.md) | 产品定位、功能范围、已确认规则和待定事项，优先阅读 |
| [初始分类目录](docs/INITIAL_CATALOG_DRAFT.md) | 岗位大类、小类和能力目录 |
| [能力等级参考](docs/ABILITY_LEVELS.md) | 77 项能力的 L0～L5 等级说明 |
| [运行预算](docs/OPERATING_BUDGET.md) | 首年 500 元预算及费用分配 |
| [页面流程与验收说明](docs/REBUILD_FLOWS_AND_ACCEPTANCE.md) | 页面组织、用户操作和验收场景 |
| [前端设计](docs/FRONTEND_DESIGN.md) | 当前重点，逐页整理页面结构、状态和交互 |
| [后端实现设计](docs/BACKEND_IMPLEMENTATION.md) | 已确认的后端范围和后续实现讨论入口 |
| [Agent 设计](docs/AGENT_DESIGN.md) | 求职 Agent 与内部 Agent 的职责、记忆、工具和评测 |

## 当前讨论状态

重要决策继续逐项与用户确认。当前按业务闭环逐步实现后端；项目推荐来源与筛选机制暂缓讨论，Agent 细节留在对应文档中后续完善。

旧项目位于相邻的 `../jobpilot/`，现有的 `../jobpilot-rebuild/` 也保留原状。本轮新版开发以当前 `jobpilot-next/` 为工作目录，旧项目作为参考。
