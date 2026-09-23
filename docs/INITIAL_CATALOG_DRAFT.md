# JobPilot 初始目录草案

岗位与能力目录、等级尺度已确认，完整等级参考已整理 · 2026 年 9 月 10 日

这份文档记录项目初始化时使用的岗位分类和能力项。九个岗位大类、28 个岗位小类、复合目标的处理规则、首批能力大类和 77 项能力已经导入数据库。每个岗位小类另在数据库迁移中维护分类定义、典型职责信号、排除信号和易混淆分类规则；77 项能力的完整等级说明按确认过的尺度整理在[能力等级参考](ABILITY_LEVELS.md)。

## 已确认的岗位目录

岗位大类按主要工作方向划分，小类描述该方向下的具体工作侧重点。初始目录如下：

| 岗位大类 | 初始小类 | 主要区分什么 |
| --- | --- | --- |
| 后端开发 | 业务后端、服务框架与中间件、开发者工具与平台后端 | 实现业务服务，或建设供其他开发者使用的服务端能力 |
| 前端开发 | Web 与小程序前端、前端工程化与组件平台、可视化与图形前端 | 实现网页交互，或建设前端基础能力 |
| 客户端开发 | 移动客户端、桌面客户端、跨端客户端 | 开发安装在用户设备上的应用及其客户端框架 |
| AI 应用开发 | RAG 与知识库应用、Agent 应用、AI 应用平台 | 将模型能力组织成可使用的产品、工作流或应用平台 |
| 数据开发 | 离线数据与数仓、实时数据处理、数据平台与治理 | 建设数据处理链路、数据模型及数据服务 |
| 测试开发 | 自动化测试、性能与稳定性测试、测试工具与平台 | 编写测试程序、验证系统质量或建设测试平台 |
| 基础软件与云平台开发 | 数据库与存储研发、操作系统与编译运行时、云平台与基础设施、AI 基础设施 | 研发底层软件、计算资源平台或模型训练推理基础设施 |
| 嵌入式开发 | 固件与驱动、嵌入式系统、设备应用 | 开发设备侧软件及软硬件交互功能 |
| 游戏开发 | 游戏客户端、游戏服务端、游戏引擎与工具 | 实现游戏运行逻辑、交互、联机服务或开发工具 |

这批目录用于启动系统，不把未列出的方向永久排除。已有目录不足以覆盖实际 JD 时，继续使用需求文档中已确定的申请、平台 Agent 审核和入库流程。

## 分类时怎样使用

沿用已确定的做法：一份 JD 可以关联多个大类及小类，保存主导方向、次要关联和原文依据。分类根据主要职责判断，岗位标题作为参考。

编程语言、框架和工具放在能力目录中，不为每种组合另建岗位分类。例如 Java 后端、Go 后端都可以对应后端开发，再通过能力项体现技术要求。实习、校招和社招仍单独保存为求职类型。

下面用虚构 JD 演示这种分法，均不代表真实招聘信息：

| JD 主要内容 | 建议归类 |
| --- | --- |
| 用 Go 开发订单和支付服务，使用 MySQL、Redis | 后端开发 → 业务后端；Go、MySQL、Redis 进入能力清单 |
| 主要建设企业知识库与 RAG 问答，同时负责配套 API | AI 应用开发 → RAG 与知识库应用为主，后端开发为次要关联 |
| 开发 MySQL 数据库内核和存储引擎 | 基础软件与云平台开发 → 数据库与存储研发；依据研发对象区分于在业务中使用 MySQL |

重叠方向通过主次关联表达。例如 AI 应用平台与 AI 基础设施，以主要工作是在组织应用能力，还是研发训练推理与资源平台为区分依据。目录中的名称不替代对 JD 原文的判断。

## 已确认的复合目标规则

“后端＋Agent 开发，校招”作为一个求职目标，关联后端开发、AI 应用开发下的 Agent 应用及校招类型。不为每种组合单独新增岗位分类。

符合该目标的 JD 必须同时实质涉及后端和 Agent 开发，且求职类型符合。可以以后端为主，也可以以 Agent 应用为主，JD 自身的主次关系仍然保存。普通后端岗位只写“了解大模型者优先”，保留参考，不计入该目标。

至少 10 份的门槛只统计处理完成且符合整个复合目标的 JD，不用两组分别符合单一方向的 JD 拼凑数量。能力清单从这些符合条件的 JD 汇总，重复能力合并，MySQL 等完整能力项仍各自只评一个综合等级。

## 能力目录继续沿用的约定

岗位目录描述用户想从事什么工作，能力目录描述该工作要求掌握什么。两者分别维护，通过 JD 中的实际要求关联。

MySQL 这样的能力项只保留一个 L0–L5 综合等级，不继续拆成索引、事务等单独评分项。每个完整能力项保存具体的等级说明，供 JD 分析、个人评估和用户自评共用。目录中存在的能力不会自动成为所有用户的必测项。

岗位小类不自动成为能力测评项。同一个能力项供不同岗位共用。

## 已确认的首批能力目录

以下是已确认的 12 个能力大类和 77 项能力。大类用于组织内容，每个具体能力项各自保存一个 L0–L5 综合等级，具体说明见[能力等级参考](ABILITY_LEVELS.md)。

| 能力大类 | 首批能力项 |
| --- | --- |
| 编程语言 | C、C++、Java、Go、Python、JavaScript、TypeScript、C#、Rust、Kotlin、Swift、ArkTS |
| 计算机基础 | 数据结构与算法、操作系统、计算机网络、计算机组成原理、数据库原理 |
| 后端技术 | Spring Boot、Spring Cloud、Gin、FastAPI、Node.js |
| 数据库与中间件 | MySQL、PostgreSQL、Redis、MongoDB、Elasticsearch、Kafka、RabbitMQ |
| 前端技术 | HTML、CSS、Vue、React、Vite、Webpack |
| 客户端技术 | Android 开发、iOS 开发、HarmonyOS 开发、Flutter、React Native、Qt、Electron |
| AI 开发 | 大模型基础、RAG、Agent 开发、LangChain、LangGraph、PyTorch、模型部署与推理 |
| 数据工程 | Hadoop、Hive、Spark、Flink、数据仓库、数据治理 |
| 工程与部署 | Git、Linux、Docker、Kubernetes、CI/CD、分布式系统、软件设计 |
| 测试技术 | 测试理论与方法、pytest、JUnit、Playwright、JMeter |
| 基础软件与嵌入式 | Linux 内核、编译原理、网络编程、STM32、FreeRTOS、嵌入式 Linux |
| 游戏与图形 | Unity、Unreal Engine、Cocos Creator、计算机图形学 |

这是用于初始化的能力目录，不代表某个用户需要掌握整张表。用户的实际测评清单只来自当前目标的相关 JD；目录不足时，按已确认的新增能力审核流程补充。

同义名称统一到同一项，例如 Golang 对应 Go、K8s 对应 Kubernetes。语言、工具和理论主题各自按含义匹配，不自动把一个已评等级套给另一项：会使用 MySQL 不等于数据库原理已经评估，会使用 Spring Boot 也不直接确定 Java 等级。已有材料可以同时为不同能力提供评估依据。

例如，一组符合“后端＋Agent”目标的 JD 只要求 Go、MySQL、Redis、Agent 开发、Docker，就先汇总这五项。若同一 JD 还具体提到 MySQL 索引或事务，相关原文仍放在 MySQL 的判断依据下，不增加细分能力评分项。

## 能力等级说明

MySQL 和 Agent 开发的六档示例及描述尺度已经确认，其余能力按同样尺度补齐为初稿。全部说明集中放在[能力等级参考](ABILITY_LEVELS.md)，方便查阅和维护。

这些说明用于判断一个综合等级，不拆成知识点分数，也不作为逐条打勾的测评清单。

模型根据用户材料和补充问答给出整体判断及依据，用户仍可以自行设置或修改等级。“尚未评估”继续单独标记，不等于 L0。

## 参考资料

本稿参考企业公开招聘页面中的职责和技能要求，再为 JobPilot 整理目录。岗位分类和能力列表已经用户确认；二者均未按岗位数量统计排序，也不是某家企业的官方分类标准。等级说明是按已确认尺度编写的产品参考。以下页面仅用于准备初始目录，不直接作为用户市场画像的数据，也不把其中的资深岗位要求设成所有用户的准备目标。

- [腾讯 AI IT Engineer Intern](https://tencent.wd1.myworkdayjobs.com/en-US/Tencent_Careers/job/AI-IT-Engineer-Intern_R106786)：职责涉及 RAG、Agent、数据处理和系统集成，用于参考 AI 应用与其他开发方向的交叉。
- [腾讯 Software Engineer — Infrastructure](https://tencent.wd1.myworkdayjobs.com/en-US/Tencent_Careers/job/Singapore-CapitaSky/WXG---Software-Engineer---Infrastructure_R107473-2)：职责涉及基础设施软件、开发平台、自动化和可观测性工具，用于参考平台开发方向。
- [腾讯 Senior Software Engineer](https://tencent.wd1.myworkdayjobs.com/en-US/tencent_careers/job/Singapore-CapitaSky/Senior-Software-Engineer_R108004)：职责涉及实时与批处理数据链路，用于参考数据开发方向。
- [华为公开岗位方向](https://career.huawei.com/reccampportal/globle/huawei-special-recruitment.html)：列有嵌入式、操作系统、编译器与编程语言开发方向，用于参考系统软件与设备软件的区分。
- [华为软件岗位技能要求](https://career.huawei.com/reccampportal/portal5/social-recruitment-detail.html?dataSource=1&jobId=27794)：分别列出编程语言、开发工具、软件设计及系统基础知识，用于参考能力内容的组织。
- [腾讯 Software Engineering Intern](https://tencent.wd1.myworkdayjobs.com/en-US/Tencent_Careers/job/Software-Engineering-Intern_R107162-1)：涉及开发语言、客户端工程以及 Unity、Unreal 等游戏引擎，用于参考客户端与游戏相关能力。
