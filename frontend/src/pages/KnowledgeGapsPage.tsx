import { useEffect, useMemo, useState } from 'react'
import {
  ArrowLeft,
  ArrowRight,
  BookOpenCheck,
  BriefcaseBusiness,
  CheckCircle2,
  ChevronRight,
  ClipboardCheck,
  Eye,
  GraduationCap,
  LockKeyhole,
  MessageSquareText,
  Play,
  RefreshCw,
  RotateCcw,
  Send,
  UserRound,
  X,
} from 'lucide-react'

interface KnowledgeGapsPageProps {
  onOpenMarket: () => void
  onOpenProfile: () => void
}

interface GapEvidence {
  source: string
  content: string
}

interface KnowledgeGap {
  id: string
  name: string
  currentLevel: string
  targetLevel: string
  currentDescription: string
  targetDescription: string
  sourceSummary: string
  userEvidence: GapEvidence[]
  marketEvidence: GapEvidence[]
}

interface InterviewQuestion {
  prompt: string
  context: string
  hint: string
  reference: string
}

type KnowledgeSection = 'overview' | 'interview'
type InterviewMode = 'verify' | 'review'

const knowledgeGaps: KnowledgeGap[] = [
  {
    id: 'mysql',
    name: 'MySQL',
    currentLevel: 'L2',
    targetLevel: 'L3',
    currentDescription: '能够在项目中完成表结构设计、CRUD、分页筛选和常见数据归属校验。',
    targetDescription: '能够在常见业务场景中独立使用 MySQL，并结合约束说明设计和处理思路。',
    sourceSummary: '简历与项目经历 · 6 份示例 JD 支持 L3',
    userEvidence: [
      { source: '后端开发实习简历', content: '在 Go 项目中使用 MySQL、手写 SQL 和 GORM 完成基础数据读写。' },
      { source: '项目经历自述', content: '能够解释表结构、分页筛选和多用户数据归属校验。' },
    ],
    marketEvidence: [
      { source: '示例 JD 02', content: '负责业务数据库设计及常见 SQL 问题处理。' },
      { source: '示例 JD 06', content: '要求能够独立使用 MySQL 支撑后端业务开发。' },
    ],
  },
  {
    id: 'rag',
    name: 'RAG',
    currentLevel: 'L2',
    targetLevel: 'L3',
    currentDescription: '能够参考资料搭建基础检索问答流程，并说明切分、召回和 Prompt 拼接。',
    targetDescription: '能够独立完成常见 RAG 场景，并基于可观察结果定位和改进效果问题。',
    sourceSummary: '经历自述 · 5 份示例 JD 支持 L3',
    userEvidence: [
      { source: '项目经历自述', content: '做过大模型聊天 Demo，接触文档切分、Embedding、向量检索和召回。' },
    ],
    marketEvidence: [
      { source: '示例 JD 01', content: '参与企业知识库问答应用的检索链路开发与效果优化。' },
      { source: '示例 JD 08', content: '能够完成 RAG 应用开发，并分析召回与回答效果。' },
    ],
  },
  {
    id: 'linux',
    name: 'Linux',
    currentLevel: 'L2',
    targetLevel: 'L3',
    currentDescription: '能够使用常见命令运行服务、查看日志并处理基础环境问题。',
    targetDescription: '能够在常见服务部署和排障场景中独立操作，并解释主要判断过程。',
    sourceSummary: '简历 · 7 份示例 JD 支持 L3',
    userEvidence: [
      { source: '后端开发实习简历', content: '具有 Linux 环境下运行服务和查看日志的基础经验。' },
    ],
    marketEvidence: [
      { source: '示例 JD 03', content: '熟悉 Linux 环境，能够完成服务部署及常见问题排查。' },
    ],
  },
  {
    id: 'software-design',
    name: '软件设计',
    currentLevel: 'L2',
    targetLevel: 'L3',
    currentDescription: '能够按已有分层结构拆分后端功能，并维护清晰的调用链路。',
    targetDescription: '能够独立完成常见模块设计，并结合需求解释边界和技术取舍。',
    sourceSummary: '代码仓库 · 6 份示例 JD 支持 L3',
    userEvidence: [
      { source: 'Go 项目代码仓库', content: '已实现路由、鉴权、Handler、Service、Repository、数据库和缓存分层。' },
    ],
    marketEvidence: [
      { source: '示例 JD 04', content: '参与后端模块设计，能够独立负责功能开发与迭代。' },
      { source: '示例 JD 07', content: '具备良好的系统设计意识和模块拆分能力。' },
    ],
  },
]

const interviewQuestions: Record<string, InterviewQuestion[]> = {
  mysql: [
    {
      prompt: '一个订单列表接口的数据量从十万增长到五百万后明显变慢。你会按照什么顺序定位问题？',
      context: '请结合真实排查步骤回答，说明你会先看什么、怎样验证判断。',
      hint: '可以从请求、SQL、执行计划、索引和数据量变化几个方向组织回答。',
      reference: '较完整的回答会先确认慢点位于数据库，再取得实际 SQL 和参数，使用 EXPLAIN / EXPLAIN ANALYZE 检查访问方式、扫描行数和排序情况，随后结合索引与查询条件验证优化效果。',
    },
    {
      prompt: '订单表按 user_id 查询并按 created_at 倒序分页，你会怎样设计索引？还需要确认哪些条件？',
      context: '不要求直接给唯一答案，重点说明索引选择与业务查询之间的关系。',
      hint: '考虑过滤列、排序列、分页方式、字段选择和实际数据分布。',
      reference: '通常会从 user_id 与 created_at 的联合索引开始验证，同时确认查询字段、回表成本、时间范围、数据分布以及深分页问题，再用执行计划和压测结果判断是否合适。',
    },
    {
      prompt: '创建订单和扣减库存需要保持一致性，你会怎样划分事务边界，并处理并发更新？',
      context: '请说明失败时的结果，以及为什么选择这种处理方式。',
      hint: '可以考虑事务范围、锁、条件更新、重试与幂等。',
      reference: '回答应说明需要原子完成的数据库操作、失败回滚、避免长事务，并结合条件更新或合适的锁控制超卖；若涉及跨服务，还需区分本地事务与分布式一致性问题。',
    },
    {
      prompt: '什么情况下普通索引已经存在，查询仍可能不走这个索引？你会怎样确认原因？',
      context: '请给出常见原因，并说明验证方法。',
      hint: '考虑选择性、隐式转换、函数、范围、统计信息和成本估算。',
      reference: '可能原因包括低选择性、隐式类型转换、对索引列使用函数、联合索引不匹配或优化器判断全表扫描成本更低。应通过实际 SQL、参数、执行计划和统计信息逐项确认。',
    },
    {
      prompt: '两个事务以不同顺序更新相同的两行数据时可能发生什么？线上出现后你会怎样处理？',
      context: '请同时回答原理、短期恢复和长期预防。',
      hint: '关注死锁检测、事务回滚、重试和固定访问顺序。',
      reference: '可能发生死锁，数据库通常会回滚其中一个事务。应用需要识别可重试错误并控制重试，同时缩短事务、固定资源访问顺序，并通过死锁日志定位具体 SQL。',
    },
    {
      prompt: '如果让你评审一张新业务表，你会检查哪些设计问题？',
      context: '请从业务约束、数据正确性和后续维护三个角度回答。',
      hint: '可考虑主键、字段类型、空值、唯一约束、索引、时间字段与数据生命周期。',
      reference: '应结合业务含义检查主键与唯一约束、字段类型和长度、NULL 语义、默认值、必要索引、时间字段、字符集、数据增长与归档，并避免只按当前接口机械建表。',
    },
  ],
  rag: [
    {
      prompt: '用户反馈知识库里明明有答案，但系统经常检索不到。你会怎样定位问题？',
      context: '请按可执行的排查顺序回答。',
      hint: '先区分入库、切分、召回、重排和生成环节。',
      reference: '先建立可复现问题并检查文档是否正确入库，再查看切分结果、查询改写、召回候选及分数，随后检查过滤、重排和上下文拼接，使用标注样本分别评估检索与生成。',
    },
    {
      prompt: '同一份长文档应该怎样切分？你会用什么结果判断切分策略是否合适？',
      context: '不要只列参数，要解释取舍。',
      hint: '考虑语义边界、上下文完整性、重叠和召回评测。',
      reference: '应结合文档结构和问答粒度选择语义边界、块大小与重叠，保留标题等上下文，并通过真实问题的召回率、命中片段完整性和最终回答效果比较方案。',
    },
    {
      prompt: '向量召回与关键词召回各自容易漏掉什么？你会怎样组合它们？',
      context: '请给出适用场景和组合方式。',
      hint: '关注专有名词、精确匹配、语义表达和融合排序。',
      reference: '向量召回擅长语义相似但可能漏掉编号或专名，关键词召回适合精确词但难覆盖改写表达。可以并行召回后做分数归一或排名融合，再交给重排模型。',
    },
    {
      prompt: 'RAG 回答引用了错误文档，但语言非常流畅。你会怎样减少这种问题？',
      context: '请分别考虑检索、生成约束和结果校验。',
      hint: '关注证据门槛、引用绑定、拒答和评测。',
      reference: '需要提高检索与重排质量，设置证据相关性门槛，让生成内容绑定可追踪片段，证据不足时拒答，并用带引用标注的数据评估事实一致性。',
    },
    {
      prompt: '你会怎样为一个企业知识库准备最小可用的 RAG 评测集？',
      context: '请说明数据来源、标注内容和指标。',
      hint: '考虑真实问题、应命中文档、参考答案和困难样本。',
      reference: '从真实业务问题和文档构造覆盖常见、边界与无答案场景的样本，标注相关文档或片段及参考要点，分别衡量检索命中、排序、回答正确性和引用一致性。',
    },
    {
      prompt: '文档更新后，旧内容仍被检索出来。你会怎样设计更新和删除链路？',
      context: '请说明一致性、可追踪和失败恢复。',
      hint: '考虑文档版本、分块标识、索引更新和后台任务。',
      reference: '可为文档和分块保留稳定标识与版本，通过后台任务重建并原子切换有效版本，删除旧分块，同时记录任务状态以处理部分失败和重试。',
    },
  ],
  linux: [
    {
      prompt: '服务接口突然大量超时，你登录 Linux 服务器后会先做哪些检查？',
      context: '请按优先顺序说明命令、观察项和判断。',
      hint: '从进程、资源、连接、日志和依赖逐步缩小范围。',
      reference: '先确认影响范围和进程状态，再查看 CPU、内存、磁盘与负载，检查连接和端口，结合应用及系统日志定位异常，并核对下游依赖，避免一开始直接重启丢失现场。',
    },
    {
      prompt: '磁盘空间报警，但删除日志后空间没有立刻释放，可能是什么原因？',
      context: '请说明怎样确认和处理。',
      hint: '关注仍被进程持有的已删除文件。',
      reference: '进程可能仍打开已删除文件，可通过 lsof 检查 deleted 文件及持有进程，安全地重启或让进程重新打开日志后释放空间，并修复日志轮转配置。',
    },
    {
      prompt: '一个进程 CPU 使用率很高，你会怎样进一步定位到具体问题？',
      context: '请从系统层逐步走到应用层。',
      hint: '考虑线程、调用栈、日志和可复现性。',
      reference: '先确认进程和持续时间，再查看线程级 CPU，结合语言工具、性能分析或调用栈找到热点，同时关联日志和请求流量，最后在可控环境复现并验证修改。',
    },
    {
      prompt: '服务由 systemd 管理，但启动后马上退出。你会怎样排查？',
      context: '请说明配置、日志和运行环境方面的检查。',
      hint: '关注 unit 状态、journal、用户权限、路径和环境变量。',
      reference: '查看 systemctl status 与 journalctl 日志，核对 ExecStart、WorkingDirectory、运行用户、权限和环境变量，并使用相同用户手动执行命令确认差异。',
    },
    {
      prompt: '端口已经监听，但另一台机器无法访问服务。你会检查哪些层面？',
      context: '请按网络路径逐层定位。',
      hint: '考虑监听地址、本机访问、防火墙、路由与安全策略。',
      reference: '确认监听在正确地址而非仅回环接口，测试本机访问，再检查主机防火墙、云安全组、容器端口映射、路由和上游代理，并在路径两端验证连接。',
    },
    {
      prompt: '怎样安全地排查一个正在运行的生产服务，而不扩大故障？',
      context: '请说明你的操作原则和证据保留方式。',
      hint: '关注只读检查、变更记录、回滚和现场信息。',
      reference: '优先执行低风险只读检查并记录时间线和指标，保留日志与现场，任何变更都应评估影响、准备回滚并逐步验证，避免无依据重启或同时改多个变量。',
    },
  ],
  'software-design': [
    {
      prompt: '一个订单模块同时处理参数校验、业务规则、数据库和外部支付，你会怎样划分代码职责？',
      context: '请说明边界与依赖方向。',
      hint: '考虑接口层、业务层、数据访问和外部适配。',
      reference: '接口层负责协议和输入输出，业务层表达用例与规则，数据访问封装持久化，外部支付通过接口与适配器隔离；依赖应指向稳定抽象，事务边界由业务用例控制。',
    },
    {
      prompt: '什么时候应该抽象公共组件，什么时候保留重复代码反而更安全？',
      context: '请结合变化原因回答。',
      hint: '关注表面相似与真正共同变化。',
      reference: '只有行为和变化原因稳定一致时才适合抽象；若两段代码只是当前形状相似、业务规则可能独立变化，过早抽象会形成耦合，可以先保留少量重复等待模式稳定。',
    },
    {
      prompt: '新增一种通知渠道时，怎样避免在业务代码里到处增加 if/else？',
      context: '请描述接口、选择和扩展方式。',
      hint: '考虑策略接口、注册和依赖注入。',
      reference: '定义稳定的通知接口，由不同渠道实现，通过配置或注册表选择实现，并在业务层依赖接口；同时明确各渠道能力差异，避免为统一形式隐藏真实约束。',
    },
    {
      prompt: '一个 Service 方法越来越长，你会依据什么决定怎样拆分？',
      context: '不要只回答“拆成小函数”。',
      hint: '考虑业务步骤、变化原因、事务和可测试边界。',
      reference: '先识别用例中的业务阶段、不同变化原因和外部依赖，在不破坏事务与一致性的前提下提取明确职责，并让拆分后的命名、输入输出和测试边界反映业务含义。',
    },
    {
      prompt: '缓存逻辑应该放在哪一层？你会怎样避免业务代码被缓存细节污染？',
      context: '请结合一致性要求说明。',
      hint: '考虑仓储装饰、专用服务、失效策略和业务可见性。',
      reference: '可在数据访问接口外使用缓存装饰器或专用缓存协调层，但涉及强一致或业务语义的失效仍需由用例明确控制；设计时应把命中、回源和失效策略变成可测试行为。',
    },
    {
      prompt: '你接手一个没有测试的旧模块，准备重构前会先做什么？',
      context: '请说明怎样控制行为变化风险。',
      hint: '关注现有行为、边界测试、小步修改和观察指标。',
      reference: '先梳理调用方和现有行为，为关键路径补充特征测试或端到端保护，记录可观察基线，再以小步提交重构并持续比较结果，避免同时修改结构和业务规则。',
    },
  ],
}

export function KnowledgeGapsPage({ onOpenMarket, onOpenProfile }: KnowledgeGapsPageProps) {
  const [activeSection, setActiveSection] = useState<KnowledgeSection>('overview')
  const [showResults, setShowResults] = useState(true)
  const [selectedGapId, setSelectedGapId] = useState<string | null>(null)
  const [notice, setNotice] = useState('')
  const [interviewMode, setInterviewMode] = useState<InterviewMode>('verify')
  const [interviewAbilityId, setInterviewAbilityId] = useState('mysql')
  const [sessionStarted, setSessionStarted] = useState(false)
  const [questionIndex, setQuestionIndex] = useState(0)
  const [savedAnswers, setSavedAnswers] = useState<string[]>([])
  const [answer, setAnswer] = useState('')
  const [showHint, setShowHint] = useState(false)
  const [showReference, setShowReference] = useState(false)
  const [sessionMessage, setSessionMessage] = useState('')

  const selectedGap = useMemo(
    () => knowledgeGaps.find((item) => item.id === selectedGapId) ?? null,
    [selectedGapId],
  )

  const interviewAbility = useMemo(
    () => knowledgeGaps.find((item) => item.id === interviewAbilityId) ?? knowledgeGaps[0],
    [interviewAbilityId],
  )

  const currentQuestions = interviewQuestions[interviewAbility.id]
  const currentQuestion = currentQuestions[questionIndex]
  const sessionComplete = questionIndex >= currentQuestions.length

  useEffect(() => {
    if (!selectedGap) return
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setSelectedGapId(null)
    }
    window.addEventListener('keydown', closeOnEscape)
    return () => window.removeEventListener('keydown', closeOnEscape)
  }, [selectedGap])

  const reanalyze = () => {
    setNotice('重新分析原型已触发；真实任务接入后，新结果生成成功前会继续保留当前结果。')
  }

  const resetSession = () => {
    setQuestionIndex(0)
    setSavedAnswers([])
    setAnswer('')
    setShowHint(false)
    setShowReference(false)
    setSessionMessage('')
  }

  const selectInterviewMode = (mode: InterviewMode) => {
    setInterviewMode(mode)
    setSessionStarted(false)
    resetSession()
  }

  const selectInterviewAbility = (id: string) => {
    setInterviewAbilityId(id)
    setSessionStarted(false)
    resetSession()
  }

  const startSession = () => {
    resetSession()
    setSessionStarted(true)
    requestAnimationFrame(() => window.scrollTo({ top: 0, behavior: 'smooth' }))
  }

  const submitAnswer = () => {
    const trimmedAnswer = answer.trim()
    if (!trimmedAnswer) {
      setSessionMessage('先写下你的回答，再进入下一题。')
      return
    }

    setSavedAnswers((current) => [...current, trimmedAnswer])
    setQuestionIndex((current) => current + 1)
    setAnswer('')
    setShowHint(false)
    setShowReference(false)
    setSessionMessage('')
  }

  return (
    <div className="page-content gaps-page">
      <div className="page-heading-row">
        <div>
          <h1>知识短板</h1>
          <p className="page-description">
            {activeSection === 'overview'
              ? '对照目标岗位要求，查看哪些完整能力需要补强。'
              : '用 Agent 进行逐级能力验证，或者按岗位要求复习八股问题。'}
          </p>
        </div>
        {activeSection === 'overview' && (
          <div className="gaps-heading-actions">
            <button className="button prototype-state-button" type="button" onClick={() => { setShowResults((current) => !current); setNotice('') }}>
              <Eye size={15} aria-hidden="true" />
              {showResults ? '查看未解锁状态' : '查看结果示例'}
            </button>
            {showResults ? (
              <button className="button button-primary gaps-analyze-button" type="button" onClick={reanalyze}>
                <RefreshCw size={15} aria-hidden="true" />
                重新分析
              </button>
            ) : (
              <button className="button button-primary gaps-analyze-button" type="button" disabled>
                <BookOpenCheck size={16} aria-hidden="true" />
                分析短板
              </button>
            )}
          </div>
        )}
      </div>

      <div className="knowledge-section-tabs" role="tablist" aria-label="知识短板页面">
        <button
          className={activeSection === 'overview' ? 'is-active' : ''}
          type="button"
          role="tab"
          aria-selected={activeSection === 'overview'}
          onClick={() => setActiveSection('overview')}
        >
          短板总览
        </button>
        <button
          className={activeSection === 'interview' ? 'is-active' : ''}
          type="button"
          role="tab"
          aria-selected={activeSection === 'interview'}
          onClick={() => { setActiveSection('interview'); setNotice('') }}
        >
          面试问答
        </button>
      </div>

      {notice && (
        <div className="market-notice" role="status">
          <CheckCircle2 size={17} aria-hidden="true" />
          <span>{notice}</span>
          <button type="button" onClick={() => setNotice('')} aria-label="关闭提示"><X size={15} /></button>
        </div>
      )}

      {activeSection === 'overview' ? (!showResults ? (
        <>
          <section className="result-readiness" aria-labelledby="gaps-readiness-title">
            <div className="readiness-summary">
              <span className="readiness-lock" aria-hidden="true"><LockKeyhole size={20} /></span>
              <div>
                <span className="eyebrow">分析条件</span>
                <h2 id="gaps-readiness-title">完成两类画像后再分析短板</h2>
                <p>当前还需补充 2 份相关 JD，并完成 3 项岗位能力评估。</p>
              </div>
            </div>

            <div className="readiness-requirements">
              <article>
                <div className="requirement-heading">
                  <span className="requirement-icon" aria-hidden="true"><BriefcaseBusiness size={17} /></span>
                  <div><strong>市场画像</strong><span>用于确定常见准备等级</span></div>
                  <b>8 / 10</b>
                </div>
                <div className="requirement-meter" aria-hidden="true"><span style={{ width: '80%' }} /></div>
                <p>达到门槛还需要 2 份符合当前目标的完整 JD。</p>
                <button className="button" type="button" onClick={onOpenMarket}>补充 JD <ArrowRight size={14} /></button>
              </article>

              <article>
                <div className="requirement-heading">
                  <span className="requirement-icon" aria-hidden="true"><UserRound size={17} /></span>
                  <div><strong>用户画像</strong><span>用于确定用户当前等级</span></div>
                  <b>13 / 16</b>
                </div>
                <div className="requirement-meter" aria-hidden="true"><span style={{ width: '81.25%' }} /></div>
                <p>3 项能力尚未评估，不能直接当作用户不会。</p>
                <button className="button" type="button" onClick={onOpenProfile}>补齐能力 <ArrowRight size={14} /></button>
              </article>
            </div>
          </section>

          <section className="gap-result-empty" aria-labelledby="gap-result-title">
            <div className="project-result-head">
              <div><h2 id="gap-result-title">短板结果</h2><p>只说明能力整体差距，不推测能力内部具体哪里薄弱。</p></div>
              <span>尚未分析</span>
            </div>
          </section>
        </>
      ) : (
        <>
          <div className="prototype-data-note">
            <span>结果页原型</span>
            <p>以下等级和依据只用于确认页面结构，不是当前账号的真实短板报告。</p>
          </div>

          <section className="gap-summary-panel" aria-labelledby="gap-summary-title">
            <div className="gap-summary-copy">
              <span className="eyebrow">当前结果</span>
              <h2 id="gap-summary-title">4 项能力需要补强</h2>
              <p>只比较完整能力的综合等级；当前结果与用于分析的画像资料一致。</p>
            </div>
            <div className="gap-summary-counts">
              <div className="is-gap"><strong>4</strong><span>需要补强</span></div>
              <div><strong>10</strong><span>达到目标</span></div>
              <div><strong>2</strong><span>要求不明确</span></div>
            </div>
          </section>

          <section className="gap-result-panel" aria-labelledby="gap-list-title">
            <div className="gap-result-heading">
              <div><h2 id="gap-list-title">需要补强的能力</h2><p>点击能力查看用户依据、市场依据和整体等级结论。</p></div>
              <span>按差距与岗位相关性排列</span>
            </div>
            <div className="gap-table-head" aria-hidden="true">
              <span>能力</span><span>用户当前等级</span><span>市场画像等级</span><span>差距</span><span>判断依据</span><span />
            </div>
            <div className="gap-list">
              {knowledgeGaps.map((gap) => (
                <button className="gap-row" type="button" key={gap.id} onClick={() => setSelectedGapId(gap.id)}>
                  <strong>{gap.name}</strong>
                  <span><b className="level-chip">{gap.currentLevel}</b></span>
                  <span><b className="target-level">{gap.targetLevel}</b></span>
                  <span className="gap-distance">需提升 1 级</span>
                  <span className="gap-source">{gap.sourceSummary}</span>
                  <ChevronRight size={16} aria-hidden="true" />
                </button>
              ))}
            </div>
          </section>
        </>
      )) : (
        <>
          <div className="prototype-data-note interview-prototype-note">
            <span>面试问答原型</span>
            <p>题目、等级和岗位依据均为页面示例；通过标准尚未确定，本原型不会修改能力画像。</p>
          </div>

          {!sessionStarted ? (
            <div className="interview-setup">
              <section className="interview-mode-section" aria-labelledby="interview-mode-title">
                <div className="interview-section-heading">
                  <div>
                    <span className="eyebrow">第一步</span>
                    <h2 id="interview-mode-title">选择问答方式</h2>
                    <p>两种模式使用相同的岗位要求，但处理回答的方式不同。</p>
                  </div>
                </div>
                <div className="interview-mode-grid">
                  <button
                    className={interviewMode === 'verify' ? 'interview-mode-card is-selected' : 'interview-mode-card'}
                    type="button"
                    onClick={() => selectInterviewMode('verify')}
                    aria-pressed={interviewMode === 'verify'}
                  >
                    <span className="interview-mode-icon" aria-hidden="true"><ClipboardCheck size={20} /></span>
                    <span className="interview-mode-copy">
                      <strong>能力验证</strong>
                      <small>闭卷完成 6 道核心题，Agent 最多追问 3 次。</small>
                    </span>
                    <span className="interview-mode-rule">只验证下一级 · 不降级</span>
                  </button>
                  <button
                    className={interviewMode === 'review' ? 'interview-mode-card is-selected' : 'interview-mode-card'}
                    type="button"
                    onClick={() => selectInterviewMode('review')}
                    aria-pressed={interviewMode === 'review'}
                  >
                    <span className="interview-mode-icon" aria-hidden="true"><GraduationCap size={20} /></span>
                    <span className="interview-mode-copy">
                      <strong>复习练习</strong>
                      <small>作答时可以查看提示、参考答案和 Agent 讲解。</small>
                    </span>
                    <span className="interview-mode-rule">仅用于复习 · 不改变等级</span>
                  </button>
                </div>
              </section>

              <section className="interview-ability-section" aria-labelledby="interview-ability-title">
                <div className="interview-section-heading">
                  <div>
                    <span className="eyebrow">第二步</span>
                    <h2 id="interview-ability-title">选择一项能力</h2>
                    <p>每轮只处理一项完整能力，不生成内部知识点等级。</p>
                  </div>
                  <span>来自当前短板结果</span>
                </div>

                <div className="interview-ability-head" aria-hidden="true">
                  <span>能力</span><span>当前等级</span><span>本轮目标</span><span>岗位依据</span><span />
                </div>
                <div className="interview-ability-list">
                  {knowledgeGaps.map((gap) => (
                    <button
                      className={interviewAbilityId === gap.id ? 'interview-ability-row is-selected' : 'interview-ability-row'}
                      type="button"
                      key={gap.id}
                      onClick={() => selectInterviewAbility(gap.id)}
                      aria-pressed={interviewAbilityId === gap.id}
                    >
                      <strong>{gap.name}</strong>
                      <span><b className="level-chip">{gap.currentLevel}</b></span>
                      <span className="interview-target-level"><b>{gap.targetLevel}</b><small>提升 1 级</small></span>
                      <span className="gap-source">{gap.sourceSummary}</span>
                      <span className="interview-select-mark" aria-hidden="true">{interviewAbilityId === gap.id ? '已选择' : '选择'}</span>
                    </button>
                  ))}
                </div>

                <div className="interview-start-bar">
                  <div>
                    <span>已选择</span>
                    <strong>{interviewMode === 'verify' ? '能力验证' : '复习练习'} · {interviewAbility.name}</strong>
                    <small>
                      {interviewMode === 'verify'
                        ? `${interviewAbility.currentLevel} → ${interviewAbility.targetLevel}，完成后等待统一判断`
                        : '可以随时查看提示和参考答案，不影响能力等级'}
                    </small>
                  </div>
                  <button className="button button-primary" type="button" onClick={startSession}>
                    <Play size={15} aria-hidden="true" />
                    开始{interviewMode === 'verify' ? '验证' : '练习'}
                  </button>
                </div>
              </section>
            </div>
          ) : (
            <div className="interview-session">
              <section className="interview-session-head">
                <button className="interview-back-button" type="button" onClick={() => setSessionStarted(false)}>
                  <ArrowLeft size={16} aria-hidden="true" />
                  返回选择
                </button>
                <div>
                  <span className="interview-session-mode">{interviewMode === 'verify' ? '能力验证' : '复习练习'}</span>
                  <h2>{interviewAbility.name}</h2>
                  <p>
                    {interviewMode === 'verify'
                      ? `当前 ${interviewAbility.currentLevel}，本轮只验证 ${interviewAbility.targetLevel}`
                      : `按照市场画像 ${interviewAbility.targetLevel} 要求进行复习`}
                  </p>
                </div>
                <div className="interview-level-route" aria-label="本轮等级">
                  <span>{interviewAbility.currentLevel}</span>
                  <ArrowRight size={15} aria-hidden="true" />
                  <strong>{interviewAbility.targetLevel}</strong>
                </div>
              </section>

              <div className="interview-workspace">
                <section className="interview-question-panel" aria-live="polite">
                  <div className="interview-progress-row">
                    <div>
                      <span>核心题进度</span>
                      <strong>{Math.min(questionIndex + 1, currentQuestions.length)} / {currentQuestions.length}</strong>
                    </div>
                    <div className="interview-progress-track" aria-hidden="true">
                      <span style={{ width: `${Math.min((savedAnswers.length / currentQuestions.length) * 100, 100)}%` }} />
                    </div>
                    <span>追问 0 / 3</span>
                  </div>

                  {sessionComplete ? (
                    <div className="interview-complete-state">
                      <span className="interview-complete-icon" aria-hidden="true"><CheckCircle2 size={24} /></span>
                      <span className="eyebrow">本轮完成</span>
                      <h3>6 道核心题已全部作答</h3>
                      <p>
                        {interviewMode === 'verify'
                          ? '通过标准还没有确定，因此原型暂不生成升级结论，也不会修改用户画像。'
                          : '本轮仅用于复习，回答记录不会改变用户能力等级。'}
                      </p>
                      <div>
                        <button className="button" type="button" onClick={() => setSessionStarted(false)}>返回能力选择</button>
                        <button className="button button-primary" type="button" onClick={startSession}><RotateCcw size={15} />重新开始</button>
                      </div>
                    </div>
                  ) : (
                    <>
                      <div className="interview-agent-question">
                        <div className="interview-question-label">
                          <span><MessageSquareText size={15} aria-hidden="true" />Agent 提问</span>
                          <b>第 {questionIndex + 1} 道核心题</b>
                        </div>
                        <h3>{currentQuestion.prompt}</h3>
                        <p>{currentQuestion.context}</p>
                      </div>

                      {interviewMode === 'review' && (
                        <div className="review-aids">
                          <button type="button" onClick={() => setShowHint((current) => !current)}>
                            {showHint ? '收起提示' : '查看提示'}
                          </button>
                          <button type="button" onClick={() => setShowReference((current) => !current)}>
                            {showReference ? '收起参考答案' : '查看参考答案'}
                          </button>
                        </div>
                      )}

                      {showHint && interviewMode === 'review' && (
                        <div className="interview-aid-box"><span>提示</span><p>{currentQuestion.hint}</p></div>
                      )}
                      {showReference && interviewMode === 'review' && (
                        <div className="interview-aid-box is-reference"><span>参考答案</span><p>{currentQuestion.reference}</p></div>
                      )}

                      <div className="interview-answer-area">
                        <label htmlFor="interview-answer">你的回答</label>
                        <textarea
                          id="interview-answer"
                          value={answer}
                          onChange={(event) => { setAnswer(event.target.value); setSessionMessage('') }}
                          placeholder={interviewMode === 'verify' ? '像真实面试一样，完整说明你的思路……' : '先写下自己的理解，再按需查看提示或参考答案……'}
                        />
                        {sessionMessage && <p className="interview-form-message" role="status">{sessionMessage}</p>}
                        <div className="interview-answer-actions">
                          <span>
                            {interviewMode === 'verify'
                              ? '本轮结束后统一反馈，作答中不显示判断。'
                              : '查看答案不会影响任何能力记录。'}
                          </span>
                          <button className="button button-primary" type="button" onClick={submitAnswer}>
                            提交并继续
                            <Send size={15} aria-hidden="true" />
                          </button>
                        </div>
                      </div>
                    </>
                  )}
                </section>

                <aside className="interview-session-aside">
                  <section>
                    <span className="eyebrow">本轮规则</span>
                    <h3>{interviewMode === 'verify' ? '只验证是否达到下一级' : '只复习，不更新画像'}</h3>
                    <ul>
                      {interviewMode === 'verify' ? (
                        <>
                          <li>6 道核心题，最多 3 道追问</li>
                          <li>追问只澄清原回答，不额外加分</li>
                          <li>通过只能上升一级，未通过不降级</li>
                          <li>结束前不展示参考答案或单题判断</li>
                        </>
                      ) : (
                        <>
                          <li>可以随时查看提示与参考答案</li>
                          <li>Agent 可以继续解释不理解的内容</li>
                          <li>不生成通过或未通过结论</li>
                          <li>不改变用户画像中的能力等级</li>
                        </>
                      )}
                    </ul>
                  </section>
                  <section>
                    <span className="eyebrow">目标依据</span>
                    <h3>市场画像 {interviewAbility.targetLevel}</h3>
                    <p>{interviewAbility.targetDescription}</p>
                    <button className="interview-evidence-link" type="button" onClick={() => { setSessionStarted(false); setActiveSection('overview'); setSelectedGapId(interviewAbility.id) }}>
                      查看等级与 JD 依据
                      <ChevronRight size={14} aria-hidden="true" />
                    </button>
                  </section>
                  <div className="interview-agent-note">
                    <MessageSquareText size={16} aria-hidden="true" />
                    <p>右下角悬浮 Agent 仍可解释当前页面；正式问答和回答记录保留在这里。</p>
                  </div>
                </aside>
              </div>
            </div>
          )}
        </>
      )}

      {activeSection === 'overview' && selectedGap && (
        <div className="drawer-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setSelectedGapId(null) }}>
          <aside className="market-drawer gap-detail-drawer" role="dialog" aria-modal="true" aria-labelledby="gap-drawer-title">
            <header className="drawer-header">
              <div><span>能力整体差距</span><h2 id="gap-drawer-title">{selectedGap.name}</h2></div>
              <button className="icon-button" type="button" onClick={() => setSelectedGapId(null)} aria-label="关闭抽屉"><X size={19} /></button>
            </header>
            <div className="drawer-body gap-detail-body">
              <div className="gap-level-comparison">
                <div><span>用户当前等级</span><strong>{selectedGap.currentLevel}</strong></div>
                <ArrowRight size={18} aria-hidden="true" />
                <div><span>市场画像等级</span><strong>{selectedGap.targetLevel}</strong></div>
              </div>

              <div className="gap-boundary-note">
                当前只能判断 {selectedGap.name} 的综合等级需要从 {selectedGap.currentLevel} 提升到 {selectedGap.targetLevel}，不会据此猜测内部具体知识点的薄弱程度。
              </div>

              <section>
                <h3>用户当前能做到什么</h3>
                <p className="gap-level-description">{selectedGap.currentDescription}</p>
                <div className="gap-evidence-list">
                  {selectedGap.userEvidence.map((evidence) => (
                    <article key={`${evidence.source}-${evidence.content}`}><span>{evidence.source}</span><p>{evidence.content}</p></article>
                  ))}
                </div>
              </section>

              <section>
                <h3>建议达到什么程度</h3>
                <p className="gap-level-description">{selectedGap.targetDescription}</p>
                <div className="gap-evidence-list is-market">
                  {selectedGap.marketEvidence.map((evidence) => (
                    <article key={`${evidence.source}-${evidence.content}`}><span>{evidence.source}</span><p>{evidence.content}</p></article>
                  ))}
                </div>
              </section>

              <section className="gap-conclusion">
                <span>整体结论</span>
                <p>{selectedGap.name} 当前综合等级低于市场画像中的常见准备等级，因此列为需要补强。</p>
              </section>
            </div>
          </aside>
        </div>
      )}
    </div>
  )
}
