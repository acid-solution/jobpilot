# JobPilot 验证指南

文档核对日期：2026-10-17；源码基线 `46970f8`。以下区分可重复方法与历史实测，不把已有测试文件或历史记录当成本次全部通过。

## 验证层次

| 层次 | 主要证据 | 限制 |
| --- | --- | --- |
| Go 单元 | 原文边界、准备门槛、状态、预算和提示词响应校验 | 不证明供应商质量 |
| PostgreSQL 集成 | 领取、事务、隔离、版本、阶段恢复及迁移 | 需要独立测试库 |
| 前端常规测试 | 状态与组件规则 | 部分浏览器用例默认跳过 |
| 无头浏览器假接口 | 轮询、候选、过期响应及问答 | 不调用真实模型或正式 API |
| 真实模型 | 固定授权语料的实际供应商结果 | 有外发、费用和样本边界 |
| 实际部署 | 镜像、账号、配置与业务结果 | CI 健康不等于全流程质量 |

## 普通命令

后端和前端的两个代码块分别从仓库根目录执行。

后端目录：

~~~powershell
Set-Location backend
go test ./...
go vet ./...
~~~

前端目录：

~~~powershell
Set-Location frontend
npm ci
npm test
npm run typecheck
npm run build
~~~

部分数据库测试未配置时跳过；真实模型默认关闭。记录运行结果时注明实际执行和跳过的范围。

## PostgreSQL 集成

准备一次性 PostgreSQL 17 + pgvector，配置：

| 变量 | 作用 |
| --- | --- |
| `JOBPILOT_TEST_DATABASE_URL` | 普通数据库集成 |
| `TEST_DATABASE_URL` | 另一组目录与审核集成 |
| `JOBPILOT_ADMIN_DATABASE_URL` | 创建、销毁独立数据库的测试 |

普通测试连接通常指向同一临时测试库，管理连接指向同一临时实例的 postgres 库。不要使用开发、线上或含个人资料的数据库。先建表再测试：

~~~powershell
go test -count=1 -v ./internal/storage/postgres -run '^TestMigrationsSmoke$'
go test -count=1 -v ./...
~~~

CI 另外使用 `-race`，并执行 `go vet`；实际步骤以[工作流](../.github/workflows/ci.yml)为准。

重点核对任务旧租约回写、失败不覆盖事实、原文版本检查、组合要求、过期确认、回执事务、推荐发布竞态、路线导入、偏好遗忘和用量归属。

## 浏览器验证

在前端目录：

~~~powershell
$env:RUN_MARKET_E2E='1'
npm run test:e2e
~~~

需要已安装的 Chromium，或通过 `PLAYWRIGHT_CHROMIUM_EXECUTABLE` 指向本机浏览器。CI 安装浏览器后显式启用。用例通过本地假接口模拟 Worker 和问答状态，不对生产页面或模型发请求。

实际线上脚本 [verify-market-live.mjs](../frontend/scripts/verify-market-live.mjs)单独受 `RUN_MARKET_LIVE` 控制，还需网站地址、仓库外测试账号文件和已授权语料文件。其重置选项会改变严格匹配的合成测试数据，不属于普通文档检查。

## 真实模型与授权

| 入口 | 显式开关 | 范围 |
| --- | --- | --- |
| JD 基础语料 | `RUN_JD_CORPUS_LIVE=1` | 固定 50 段基础阶段 |
| 实际 Worker 链路 | `RUN_JD_PIPELINE_LIVE=1` | 指定固定样本和完整队列 |
| 粒度样本 | `RUN_JD_GRANULARITY_LIVE` | 基础、followup 或 normalization_only，范围不同 |
| Agent 上下文 | `RUN_AGENT_CONTEXT_LIVE_TEST=1` | 指定测试账号和合成场景 |

仅在已明确授权对应样本、目的地和费用范围时运行。文档重写不自动开启。粒度测试的 `JD_GRANULARITY_REQUEST_LIMIT` 是单次保护，不会增加累计授权额度。

[JD 评测](JD_PARSING_EVALUATION.md)保留 50 段历史结果、五段链路、指定镜像线上记录及安恒失败轮次。安恒样本在 8 次请求后停止，后续 v18 和元数据修复没有新增真实模型调用，完整质量验收仍未完成。

[Agent 评测](AGENT_CONTEXT_MEMORY.md#验证记录)是自建合成场景，不是公开基准成绩；初次结果与只重查错误的结果不能拼成一次新完整成绩。

## 部署与文档

部署脚本测试在无网络容器使用 Docker 替身检查发布、备份失败和锁，不等于真实 SSH 发布。生产配置与业务验收见[部署入口](../deploy/README.md)。

文档更新应检查相对链接、标题锚点、代码围栏、JSON 示例、路由方法、配置名称和源码入口，并执行 `git diff --check`。测试数量和线上状态需要新的运行证据，不写成恒定属性。

### 2026-10-17 文档重整检查

两仓合计检查 44 份业务与工程文档、285 个本地链接及锚点、JSON 示例、23 段 PowerShell 和 11 段 Bash 语法；开发、单独部署、联合、HTTP 联调及镜像叠加共 7 组 Compose 配置通过静态解析。另核对路由方法、配置名及源码路径，77 项等级定义与原始评测结果保留原义。

这些检查只验证文档结构、示例语法和配置可解析，没有启动部署、调用模型、运行数据库集成测试或重验线上业务。
