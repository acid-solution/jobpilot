# JobPilot 文档导航

现行文档以仓库中的可运行实现为依据，核对日期为 2026-10-17。从[项目 README](../README.md)开始，术语以[业务词典](../GLOSSARY.md)为准。

## 阅读顺序

| 目的 | 文档 |
| --- | --- |
| 了解产品、条件与操作 | [产品说明](REBUILD_PRD.md) |
| 按页面验收主流程 | [流程与验收](REBUILD_FLOWS_AND_ACCEPTANCE.md) |
| 理解模块、边界和关键数据流 | [系统架构](../ARCHITECTURE.md) |
| 安装、配置、启动和排错 | [开发指南](DEVELOPMENT.md) |
| 阅读后端源码 | [后端入口](../backend/README.md)、[后台处理与一致性](BACKEND_IMPLEMENTATION.md) |
| 阅读前端交互 | [前端说明](FRONTEND_DESIGN.md) |
| 运行和解释测试 | [验证指南](VALIDATION.md) |
| 部署与更新 | [部署入口](../deploy/README.md)、[联合部署](../deploy/JOINT_DEPLOYMENT.md)、[CI/CD](../deploy/CICD.md) |

## 专项规则

| 功能 | 文档 |
| --- | --- |
| 招聘材料、JD 候选与分阶段分析 | [JD 处理](JD_IMPORT.md) |
| 能力粒度、范围归属与提示词契约 | [能力归一化](ABILITY_GRANULARITY_PROMPTS.md) |
| 岗位和能力目录 | [目录参考](INITIAL_CATALOG_DRAFT.md) |
| 六档等级说明 | [能力等级参考](ABILITY_LEVELS.md) |
| 身份、受众与两个网站 | [认证接入](SHARED_AUTH_SERVICE.md) |
| Agent 工具和写操作 | [Agent 说明](AGENT_DESIGN.md) |
| 摘要、偏好、遗忘 | [上下文与记忆](AGENT_CONTEXT_MEMORY.md) |
| 两份计划与跨系统导入 | [学习路线](LEARNING_ROUTES.md) |
| 调用与费用统计 | [模型用量](MODEL_USAGE.md) |
| 资源和支出边界 | [预算说明](OPERATING_BUDGET.md) |

## 决策与记录

- [材料识别与分阶段分析分离](adr/0001-separate-jd-import-and-analysis.md)：保留这一领域边界的原因。
- [JD 真实模型评测](JD_PARSING_EVALUATION.md)：记录特定语料、版本和失败，不能用历史部分通过代表当前完整流水线。
- [Agent 实测记录](AGENT_CONTEXT_MEMORY.md#验证记录)：区分合成场景、真实模型调用和页面验收。

## 如何维护

1. 词典只写业务定义；字段、SQL、并发和协议放到架构或专项文档。
2. 产品文档写当前行为，未来设想明确标为后续范围。
3. 一项规则只在对应专项说明中详细定义，其余页面链接过去。
4. 改变流程时一起核对产品、接口、配置与验证入口；保留已有公开文件路径，避免旧链接失效。
5. 测试记录写清版本、环境与样本范围。没有运行的验证不写成通过。
6. 文档及示例只使用占位值，不包含本机账号、真实简历、Key、Cookie 或线上运行配置。
