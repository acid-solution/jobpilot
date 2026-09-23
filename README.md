# JobPilot

面向求职者的**项目推荐与能力短板分析**后端平台。

求职者最缺的往往不是简历模板，而是不知道该做什么项目。JobPilot 的做法是：先把目标岗位的真实要求变成一份可量化的**市场能力画像**，再把用户自己的经历变成一份**用户能力画像**，两者对齐后给出项目候选和知识短板。

> 本仓库是 JobPilot 的重开发版本。早期以技能差距与学习路线生成器为方向的实现已归档为 `jobpilot-old`，不在本仓库范围内。

## 目录

- [核心链路](#核心链路)
- [技术栈](#技术栈)
- [架构设计](#架构设计)
- [数据模型](#数据模型)
- [当前实现边界](#当前实现边界)
- [本地运行](#本地运行)
- [项目结构](#项目结构)
- [文档索引](#文档索引)

## 核心链路

```
选择求职目标（岗位目录）
        │
        ▼
粘贴 JD 原文 ──► 同一事务持久化原文 + 分析任务 ──► 返回 202
        │
        ▼
后台 Worker（租约 + 心跳）
   ├─ 语义有效性判断
   ├─ 内容解析
   ├─ 岗位分类（依据职责原文选恰好一个主导小类）
   └─ 能力归一化（按 single / any_of / at_least_n 匿名组建模）
        │
        ▼
目录外能力 ──► 去重审核申请 ──► 独立审核 Worker（平台 Key）──► 复用 / 新增 / 拒绝
        │
        ▼
有效 JD 累计 ≥ 10 份 ──► 市场能力画像完成
        │
        ▼
用户材料 ──► 证据提取 ──► 用户能力画像 ──► 能力验证 / 复习练习
```

## 技术栈

| 层 | 选型 |
|---|---|
| 语言 | Go 1.26 |
| Web 框架 | Gin 1.12 |
| 数据库 | PostgreSQL 17 + pgvector |
| 数据访问 | pgx/v5（手写 SQL，无 ORM） |
| 迁移 | Goose（服务启动时自动执行） |
| 认证 | RS256 JWT + JWKS 本地验签（golang-jwt/v5） |
| 模型 | DeepSeek Chat Completions，非思考模式 + JSON Output |
| 密钥保护 | AES-256-GCM |
| 前端 | React 19 + TypeScript 5.9 + Vite 8 |

## 架构设计

### 模块划分

后端按职责分层，依赖方向单向：`httpapi` → 领域包 → `storage/postgres`。

```
cmd/server            组装根，注入全部依赖
internal/
  httpapi             HTTP 路由、请求绑定、错误映射
  target              求职目标（必须来自岗位目录）
  jdanalysis          JD 接收、基础校验、任务投递
  market              市场画像、能力覆盖统计
  abilitygrading      单份 JD 的能力判级
  abilityreview       目录外能力的去重审核
  profile             用户画像材料、证据、练习会话
  model/deepseek      DeepSeek 客户端
  modelconfig         用户 Key 的加密存取
  secure              AES-256-GCM
  identity            JWKS 拉取与 JWT 校验
  storage/postgres    仓储实现与迁移
```

### 几个关键设计

**持久化任务用租约 + 心跳，而不是简单的状态字段。**
分析任务领取时写入租约凭证，Worker 定期续心跳。Worker 崩溃后，运行期扫描会把过期任务重新排队；旧 Worker 即使稍后返回，也无法覆盖新领取者写入的结果。任务失败按次数重试，超过上限转永久失败。

**目录外能力不直接进入公开能力列表。**
新能力先按标准名和别名做精确匹配；匹配不上时创建去重审核申请，由平台 Key 驱动的独立 Worker 返回 `reuse_existing` / `approve_new` / `reject`。批准时在一个事务内写入定义、别名和 L0–L5 六档说明，并关联所有等待中的选项；拒绝时清理候选和空要求组。审核 Worker 有自己独立的租约、心跳、过期恢复、每日账号/平台额度和用量记录。

**能力要求按匿名组建模，而不是平铺成关键词。**
一份 JD 里的能力要求保存为 `single`、`any_of`、`at_least_n` 三种匿名组，每个候选项分别归一化并保留具体限定和原文证据。后端会对模型输出再做一次约束校验。

**市场能力覆盖按 JD 去重，且区分组语义。**
`single` 能力和 `any_of` 的每个候选都能独立覆盖对应要求；`at_least_n` 的单个候选只是组合的一部分，不计为单项即可满足。统计只表达"该能力能覆盖多少份 JD"，与用户是否掌握无关。

**求职目标必须来自数据库岗位目录。**
迁移会尝试把旧的自由文本方向按名称匹配到目录；无法可靠匹配的目标标记为需要重新选择，并阻止继续提交 JD，避免错误的分类名称污染后续画像。

**模型输出的可信边界。**
所有分类都必须返回逐字证据和独立理由；每个岗位小类向模型提供定义、正向信号、排除信号和易混淆规则。用户 Key 用 AES-256-GCM 加密存储，加密密钥由 `CREDENTIAL_ENCRYPTION_KEY` 环境变量提供，不入库、不入仓。

**认证只做验签，不做签发。**
Access Token 由独立的认证服务签发，本服务通过 JWKS 本地验签，并校验 `iss`、`aud`、有效期、用户 UUID 和会话 UUID。开发环境如需绕过认证，必须显式设置 `AUTH_MODE=dev`。

## 数据模型

23 张表，按职责分组：

| 分组 | 表 |
|---|---|
| 岗位与能力目录 | `job_categories`、`job_specialties`、`target_directions`、`abilities`、`ability_categories`、`ability_levels` |
| 求职目标 | `job_targets` |
| JD 与要求 | `job_descriptions`、`job_description_classifications`、`job_description_ability_requirements`、`job_description_ability_requirement_options`、`job_description_abilities` |
| 后台任务 | `analysis_jobs`、`jd_ability_level_jobs`、`ability_review_requests`、`platform_model_usage` |
| 能力判级 | `jd_ability_level_assessments` |
| 用户画像 | `user_profile_materials`、`user_profile_evidence`、`user_capability_profiles`、`profile_practice_sessions`、`user_profile_settings` |
| 模型配置 | `model_configs` |

迁移共 22 个版本文件、1,863 行 SQL，全部由 Goose 管理，无 AutoMigrate。

## 当前实现边界

本仓库按业务闭环逐步实现后端，**未完成的部分不伪装成已完成**。

### 已连接真实后端

- 账号会话：注册、登录、密码重置、会话刷新、退出
- 目录化求职目标：创建、编辑、读取
- JD 管理：新增、批量导入（最多 50 份）、编辑、删除、重复检测、按状态/关键词/公司/能力/分类筛选搜索
- 分析任务：处理状态展示、失败重试、能力审核重试
- 岗位分类与能力归一化结果
- 单份 JD 的能力判级
- 市场能力画像
- 用户画像：材料录入与确认、证据提取、练习会话
- DeepSeek Key 配置：读取、保存、删除、连通性测试

### 仍为原型数据（尚未接入后端）

- 工作台
- 项目推荐
- 知识短板
- 悬浮求职 Agent 对话

项目推荐的来源与筛选机制仍在设计中；Agent 的工具集、记忆与评测方案见 [Agent 设计](docs/AGENT_DESIGN.md)。

## 本地运行

### 依赖

- Go 1.26+
- Node.js 24.18+
- PostgreSQL 17 + pgvector
- 一个可用的认证服务（见下）

### 认证服务

JobPilot 只做 Token 验签，签发由独立的认证服务负责，**该服务不在本仓库内**。需要它提供 `/.well-known/jwks.json`，并满足：

- 算法 RS256，`iss` 与 `aud` 可配置
- Access Token 的 `sub` 与 `sid` 为 UUID

只跑本项目做开发时，可以显式设置 `AUTH_MODE=dev` 绕过认证，此时使用 `DEV_USER_ID` 或请求头 `X-Dev-User-ID`。

### 启动

```bash
# 1. 启动数据库（启用 pgvector，映射到宿主 5433）
cd backend
docker compose up -d postgres
```

服务直接读取环境变量，**不会自动加载 `.env` 文件**，需要自行 export 或写进启动脚本。两个必填项：

| 变量 | 说明 |
|---|---|
| `DATABASE_URL` | 例：`postgres://jobpilot:jobpilot@127.0.0.1:5433/jobpilot?sslmode=disable` |
| `CREDENTIAL_ENCRYPTION_KEY` | 32 个随机字节的 Base64 文本，用于加密用户 DeepSeek Key |

```bash
# 2. 生成加密密钥并导出环境变量
export CREDENTIAL_ENCRYPTION_KEY=$(openssl rand -base64 32)
export DATABASE_URL='postgres://jobpilot:jobpilot@127.0.0.1:5433/jobpilot?sslmode=disable'
export PLATFORM_DEEPSEEK_API_KEY='<平台专用 Key，能力审核 Worker 使用>'

# 3. 启动 API（启动时自动执行 Goose 迁移）
go run ./cmd/server
```

```bash
# 4. 启动前端
cd ../frontend
npm install
npm run dev
```

前端开发服务器把 `/api` 代理到 `127.0.0.1:18081`，把 `/auth` 代理到 `127.0.0.1:18082`。

其余变量及默认值见 [`backend/.env.example`](backend/.env.example)。其中三个功能开关默认关闭，需要显式打开：`ABILITY_REVIEW_ENABLED`、`JD_ABILITY_GRADING_ENABLED`、`JOB_CLASSIFICATION_REVIEW_ENABLED`。

### 验证

```bash
cd backend && go test ./...
```

20 个测试文件、2,232 行测试代码。

## 项目结构

```
.
├── backend
│   ├── cmd/server              组装根
│   ├── internal                领域包与仓储（见架构设计）
│   ├── compose.yaml            PostgreSQL + pgvector
│   └── README.md               后端运行说明与接口表
├── frontend
│   └── src
│       ├── App.tsx             页面路由与工作台
│       ├── api.ts              类型化 API 客户端（Bearer + 401 自动刷新）
│       ├── components/         AgentWidget、AuthScreen
│       └── pages/              市场画像、用户画像、项目推荐、知识短板、设置
└── docs                        需求、设计、验收与预算文档
```

## 文档索引

| 文档 | 内容 |
|---|---|
| [需求文档](docs/REBUILD_PRD.md) | 产品定位、功能范围、已确认规则与待定事项 |
| [页面流程与验收说明](docs/REBUILD_FLOWS_AND_ACCEPTANCE.md) | 页面组织、用户操作和验收场景 |
| [后端实现设计](docs/BACKEND_IMPLEMENTATION.md) | 已确认的后端范围与实现记录 |
| [Agent 设计](docs/AGENT_DESIGN.md) | 求职 Agent 与内部 Agent 的职责、记忆、工具和评测 |
| [初始分类目录](docs/INITIAL_CATALOG_DRAFT.md) | 岗位大类、小类和能力目录 |
| [能力等级参考](docs/ABILITY_LEVELS.md) | 77 项能力的 L0–L5 等级说明 |
| [前端设计](docs/FRONTEND_DESIGN.md) | 逐页的页面结构、状态和交互 |
| [运行预算](docs/OPERATING_BUDGET.md) | 首年预算与费用分配 |
