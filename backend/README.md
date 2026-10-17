# JobPilot 后端入口

后端是 Go + Gin 单服务，包含 API、持久化 Worker、Eino Agent 和 PostgreSQL 适配。市场画像之外，还实现个人画像、问答、知识短板、项目推荐、学习路线、上下文偏好和调用统计。

## 运行与配置

本地配置、数据库启动、身份模式及功能开关统一维护在[开发指南](../docs/DEVELOPMENT.md)。从当前目录启动：

~~~powershell
go run ./cmd/server
~~~

启动顺序为：读取配置 → 连接数据库 → 执行嵌入迁移 → 组装服务与 Worker → 注册路由 → 接收 HTTP。`.env` 按启动目录读取，进程环境优先。

## 从哪里读代码

| 入口 | 作用 |
| --- | --- |
| [cmd/server/main.go](cmd/server/main.go) | 配置、仓库、服务、Worker 与路由的组合根 |
| [internal/httpapi](internal/httpapi) | 参数、身份、HTTP/SSE 和错误边界 |
| [internal/jdimport](internal/jdimport) | 原始材料与候选导入 |
| [internal/jdanalysis](internal/jdanalysis) | 提取、分类、能力归一化 |
| [internal/readiness](internal/readiness) | 统一分析门槛与组合要求判断 |
| [internal/storage/postgres](internal/storage/postgres) | SQL、事务、租约及持久化 |
| [internal/workerpool](internal/workerpool) | Worker 循环、续租与退出生命周期 |

其他模块及数据流见[系统架构](../ARCHITECTURE.md)，并发和发布保护见[后台处理](../docs/BACKEND_IMPLEMENTATION.md)。

## 检查

~~~powershell
go test ./...
go vet ./...
~~~

未配置数据库时部分集成测试会跳过；这不是数据库验证通过。真实模型测试默认关闭。隔离测试库与完整命令见[验证指南](../docs/VALIDATION.md)。
