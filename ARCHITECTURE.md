# JobPilot 架构

核对日期：2026-10-17。业务术语见[词典](GLOSSARY.md)，产品行为见[产品说明](docs/REBUILD_PRD.md)。

## 系统边界

~~~mermaid
flowchart LR
  UI[React 页面] --> API[JobPilot Go API]
  UI --> Auth[shared-auth]
  API --> PG[(PostgreSQL 与 pgvector)]
  Worker[服务内 Worker] --> PG
  Worker --> Model[DeepSeek 与百炼]
  API --> Agent[Eino Agent 与受控工具]
  Agent --> PG
  API --> Exchange[认证令牌交换]
  Exchange --> Auth
  API --> MCP[StudyFlow MCP]
  MCP --> SF[(StudyFlow 业务库)]
~~~

JobPilot 保存求职目标、岗位资料、画像、分析结果、助手会话和路线；认证服务保存账号与刷新会话；StudyFlow 保存执行计划、计时和复盘。业务服务以稳定用户 UUID 关联，不直接操作另一个服务的数据库。共享账号不代表浏览器自动单点登录。

API、Worker、Agent 位于同一 Go 模块。后台任务使用 PostgreSQL 持久化队列和租约，不依赖独立消息队列。关系表保存业务事实，向量索引辅助召回，模型负责判断与生成。

## 模块分工

| 模块 | 责任与输入输出 |
| --- | --- |
| `target`、`market` | 当前求职范围、JD 管理、目标关系与市场画像 |
| `jdimport` | 原始材料识别会话、候选与确认导入 |
| `jdanalysis`、`abilityidentity` | 内容提取、分类、标准能力归属与阶段结果 |
| `abilityreview`、`abilitygrading` | 目录候选审核与岗位能力深度判定 |
| `profile` | 个人信息、材料分析、能力水平与问答 |
| `readiness`、`knowledgegaps` | 统一准备条件和岗位要求满足情况 |
| `projectrecs` | 项目设想、GitHub 对照、选择及依据 |
| `learningroutes` | 路线版本、生成任务与固定内容的导入 |
| `agent`、`agentcontext`、`assistantmemory` | 工具对话、上下文预算、摘要、偏好 |
| `modelconfig`、`secure`、`modelusage` | 用户凭据、加密、调用用量与价格快照 |
| `storage/postgres` | 持久化、事务、领取 SQL 和迁移 |
| `httpapi`、`identity`、`workerpool` | HTTP 边界、身份和后台执行生命周期 |

源码均位于 [backend/internal](backend/internal)，组合根为 [cmd/server](backend/cmd/server/main.go)。

## 主数据流

### 岗位资料

原始材料先持久化为识别会话，再产生候选。单候选直接导入，多候选由用户选择；正式 JD 与分析任务共同提交。Worker 按原文版本保存提取、分类和能力归一化阶段，再由相关审核、判级完善结果。技术失败保留错误和旧结果，不替代有效性判断。

正式 JD 与“计入画像的 JD”是不同概念。当前目标关系决定统计范围。详细状态与兼容接口见 [JD 说明](docs/JD_IMPORT.md)。

### 画像与分析

市场画像、个人必要信息和岗位要求共同形成请求内快照。`readiness` 统一决定是否可以生成知识短板、推荐与路线，避免各模块采用不同门槛。快照不跨请求缓存；后台发布前仍在账号锁保护下重新读取并核对输入，不能以生成前快照替代发布校验。

### 助手写入

模型只能提出业务动作，用户确认后由受控工具调用已有服务。资料版本、动作状态及内容摘要用于阻止过期确认。业务写入与动作回执在受保护的事务范围中处理；工具回显不充当数据库事实。见 [Agent](docs/AGENT_DESIGN.md)。

### 学习路线

选题与学习规划分别保存。用户主动生成路线、编辑预览并确认固定内容后，服务端换取短期代办令牌，通过 MCP 导入 StudyFlow 草稿。路线版本与两份计划版本分别记录，执行进度由 StudyFlow 管理。见[路线说明](docs/LEARNING_ROUTES.md)。

## 正确性边界

- 数据访问按账号及目标范围约束；普通写操作与后台发布在必要范围内串行化。
- 任务领取、心跳、完成写回都受租约保护；租约不证明输入仍然有效。
- 模型与 GitHub 网络调用放在长期业务事务之外。
- 模型已产生用量后，即使业务失败或结果过期，用量仍保留。
- 材料编辑使旧结果需要核对，不因页面刷新自动付费重解析。
- 向量相似度只生成候选，不能自动建立别名、能力等级或用户事实。

数据库与后台细节见[后端说明](docs/BACKEND_IMPLEMENTATION.md)；身份和密钥见[认证接入](docs/SHARED_AUTH_SERVICE.md)；运行拓扑见[联合部署](deploy/JOINT_DEPLOYMENT.md)。
