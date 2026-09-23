import { useEffect, useMemo, useState } from 'react'
import {
  ArrowLeft,
  ArrowRight,
  BriefcaseBusiness,
  CheckCircle2,
  Clock3,
  Eye,
  LockKeyhole,
  RotateCcw,
  Sparkles,
  UserRound,
  X,
} from 'lucide-react'

interface ProjectsPageProps {
  onOpenMarket: () => void
  onOpenProfile: () => void
}

interface ProjectRecommendation {
  id: string
  rank: number
  type: '从零开发' | '开源二开'
  title: string
  summary: string
  duration: string
  abilities: string[]
  fitReasons: string[]
  audience: string
  problem: string
  shape: string
  scope: string[]
  knownFacts: string[]
  assumptions: string[]
}

const recommendations: ProjectRecommendation[] = [
  {
    id: 'agent-task-control',
    rank: 1,
    type: '从零开发',
    title: 'Agent 任务执行与人工确认平台',
    summary: '让需要调用多个工具的 Agent 任务可以暂停确认、失败重试，并保留完整执行记录。',
    duration: '8～10 周',
    abilities: ['Go', 'Agent 开发', 'Redis', '消息队列'],
    fitReasons: [
      '与后端开发＋Agent 应用的复合目标直接相关。',
      '可以沿用已有 Go / Gin 基础，同时补充 Agent 工程经验。',
    ],
    audience: '需要把 Agent 接入内部业务流程的小型研发团队。',
    problem: '普通聊天界面难以处理长时间任务、工具失败和高风险操作确认，执行结果也缺少可追踪记录。',
    shape: '一个包含任务状态、工具调用、人工确认、重试和审计记录的多用户 Web 系统。',
    scope: [
      '设计可暂停、恢复、失败和重试的任务状态流转。',
      '实现受控工具调用与人工确认，并记录每一步输入输出。',
      '处理异步执行、重复请求和任务恢复，提供可验证的运行结果。',
    ],
    knownFacts: ['目标岗位同时涉及后端与 Agent 应用。', '已有 Go / Gin、MySQL 和基础 Redis 项目经历。'],
    assumptions: ['实际团队是否愿意采用这套确认流程仍需访谈或场景验证。', '任务规模和模型失败类型需要通过样例任务继续收敛。'],
  },
  {
    id: 'ticket-agent-extension',
    rank: 2,
    type: '开源二开',
    title: '工单系统的 Agent 协作改造',
    summary: '以现有开源工单系统为底座，让 Agent 汇总上下文、提出处理建议，并在确认后执行操作。',
    duration: '6～8 周',
    abilities: ['Go', 'RAG', 'Agent 开发', '软件设计'],
    fitReasons: [
      '保留成熟工单流程，把个人工作集中在 Agent 与业务系统的可靠协作。',
      '改造理由来自信息分散和操作需要确认，而不是单纯增加聊天入口。',
    ],
    audience: '需要频繁查找历史工单、知识文档并协调处理的客服或运维团队。',
    problem: '处理人员需要在工单、历史记录和知识库之间反复查找信息，直接让模型执行又难以控制误操作。',
    shape: '选择一个体量合适的开源工单系统，保留核心业务，再加入上下文检索、操作建议、确认和审计链路。',
    scope: [
      '梳理原系统的数据模型和扩展边界，选择需要保留的核心流程。',
      '实现工单上下文与知识文档检索，并展示答案依据。',
      '把状态修改等写操作放入确认流程，记录 Agent 建议与实际执行结果。',
    ],
    knownFacts: ['复合目标需要后端工程和 Agent 应用证据。', '已有分层 Web API、鉴权和多用户数据隔离经验。'],
    assumptions: ['具体开源底座尚未选择，许可证、代码质量和二开成本都需要验证。', '目标团队的真实工作流需要进一步调查，当前场景只用于页面示例。'],
  },
  {
    id: 'model-runtime-console',
    rank: 3,
    type: '从零开发',
    title: '多模型调用稳定性管理台',
    summary: '统一管理多个模型服务的请求、限流、失败切换、成本记录和效果对比。',
    duration: '8～10 周',
    abilities: ['Go', '大模型基础', 'MySQL', '可观测性'],
    fitReasons: [
      '能体现后端稳定性设计，也与大模型应用工程直接相关。',
      '结果可以通过故障注入、调用记录和对比报表进行演示。',
    ],
    audience: '同时接入多个模型服务、需要控制稳定性和使用情况的 AI 应用团队。',
    problem: '业务直接调用各家模型接口时，错误处理、限流和调用记录分散，切换服务后难以比较效果。',
    shape: '提供统一调用入口、策略配置、失败处理和结果记录的后台服务与管理页面。',
    scope: [
      '封装多家模型调用并统一请求、响应和错误分类。',
      '实现限流、超时、重试和可控的服务切换策略。',
      '记录调用成本、延迟和结果，设计可重复的对比验证方式。',
    ],
    knownFacts: ['目标方向包含 AI 应用工程。', '已有 Go 后端、数据库与缓存基础。'],
    assumptions: ['不同模型的效果指标和测试集还需要确定。', '免费或低成本额度能否覆盖演示规模需要在开发前验证。'],
  },
]

export function ProjectsPage({ onOpenMarket, onOpenProfile }: ProjectsPageProps) {
  const [showResults, setShowResults] = useState(true)
  const [selectedProjectId, setSelectedProjectId] = useState<string | null>(null)
  const [chosenProjectId, setChosenProjectId] = useState<string | null>(null)
  const [showChosenProject, setShowChosenProject] = useState(false)
  const [showRegenerate, setShowRegenerate] = useState(false)
  const [adjustment, setAdjustment] = useState('')
  const [notice, setNotice] = useState('')

  const selectedProject = useMemo(
    () => recommendations.find((item) => item.id === selectedProjectId) ?? null,
    [selectedProjectId],
  )
  const chosenProject = useMemo(
    () => recommendations.find((item) => item.id === chosenProjectId) ?? null,
    [chosenProjectId],
  )
  const drawerOpen = Boolean(selectedProject || showRegenerate)

  const closeDrawer = () => {
    setSelectedProjectId(null)
    setShowRegenerate(false)
    setAdjustment('')
  }

  useEffect(() => {
    if (!drawerOpen) return
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeDrawer()
    }
    window.addEventListener('keydown', closeOnEscape)
    return () => window.removeEventListener('keydown', closeOnEscape)
  }, [drawerOpen])

  const submitRegenerate = () => {
    setNotice(adjustment.trim()
      ? `已记录本次调整意见：“${adjustment.trim()}”。原型暂不调用真实模型。`
      : '已发起按当前画像重新推荐的原型操作，当前结果会保留到新结果生成成功。')
    closeDrawer()
  }

  const chooseProject = () => {
    if (!selectedProject) return
    setChosenProjectId(selectedProject.id)
    setShowChosenProject(true)
    setNotice(`已选择“${selectedProject.title}”。`)
    closeDrawer()
  }

  return (
    <div className="page-content projects-page">
      <div className="page-heading-row">
        <div>
          <h1>{showChosenProject && chosenProject ? '已选项目' : '项目推荐'}</h1>
          <p className="page-description">
            {showChosenProject && chosenProject
              ? '查看已经选定的方向、推荐依据和开始前需要核对的事项。'
              : '结合目标岗位和个人情况，筛选值得投入、适合写进简历的项目。'}
          </p>
        </div>
        <div className="project-heading-actions">
          {showChosenProject && chosenProject ? (
            <button className="button prototype-state-button" type="button" onClick={() => { setShowChosenProject(false); setNotice('') }}>
              <ArrowLeft size={15} aria-hidden="true" />
              返回推荐列表
            </button>
          ) : (
            <>
              <button className="button prototype-state-button" type="button" onClick={() => { setShowResults((current) => !current); setNotice('') }}>
                <Eye size={15} aria-hidden="true" />
                {showResults ? '查看未解锁状态' : '查看结果示例'}
              </button>
              {showResults ? (
                <button className="button button-primary projects-generate-button" type="button" onClick={() => setShowRegenerate(true)}>
                  <RotateCcw size={15} aria-hidden="true" />
                  调整后重新推荐
                </button>
              ) : (
                <button className="button button-primary projects-generate-button" type="button" disabled>
                  <Sparkles size={16} aria-hidden="true" />
                  生成推荐
                </button>
              )}
            </>
          )}
        </div>
      </div>

      {notice && (
        <div className="market-notice" role="status">
          <CheckCircle2 size={17} aria-hidden="true" />
          <span>{notice}</span>
          <button type="button" onClick={() => setNotice('')} aria-label="关闭提示"><X size={15} /></button>
        </div>
      )}

      {showChosenProject && chosenProject ? (
        <>
          <div className="prototype-data-note">
            <span>选定页原型</span>
            <p>当前展示选择后的页面结构；项目内容仍为模拟数据。</p>
          </div>

          <section className="chosen-project" aria-labelledby="chosen-project-title">
            <div className="chosen-project-hero">
              <div className="chosen-project-copy">
                <div className="chosen-project-kicker">
                  <span className={`project-type ${chosenProject.type === '开源二开' ? 'is-extension' : ''}`}>{chosenProject.type}</span>
                  <span className="chosen-project-state"><CheckCircle2 size={14} aria-hidden="true" />已选定</span>
                </div>
                <h2 id="chosen-project-title">{chosenProject.title}</h2>
                <p>{chosenProject.summary}</p>
              </div>
              <div className="chosen-project-meta">
                <div><span>对应目标</span><strong>后端开发＋Agent 应用</strong></div>
                <div><span>预计投入周期</span><strong>{chosenProject.duration}</strong></div>
                <div><span>推荐顺序</span><strong>#{chosenProject.rank}</strong></div>
              </div>
            </div>

            <div className="chosen-project-layout">
              <div className="chosen-project-main">
                <section>
                  <span className="eyebrow">项目定义</span>
                  <h3>要解决的问题</h3>
                  <p>{chosenProject.problem}</p>
                </section>
                <section>
                  <span className="eyebrow">预期形态</span>
                  <h3>准备做成什么</h3>
                  <p>{chosenProject.shape}</p>
                </section>
                <section>
                  <span className="eyebrow">个人工作</span>
                  <h3>需要完成的主体范围</h3>
                  <ol className="chosen-project-scope">
                    {chosenProject.scope.map((item) => <li key={item}>{item}</li>)}
                  </ol>
                </section>
              </div>

              <aside className="chosen-project-aside" aria-label="选择依据">
                <section>
                  <h3>为什么适合你</h3>
                  <ul>
                    {chosenProject.fitReasons.map((reason) => <li key={reason}>{reason}</li>)}
                  </ul>
                </section>
                <section>
                  <h3>可以体现的能力</h3>
                  <div className="project-skill-tags is-detail">
                    {chosenProject.abilities.map((ability) => <span key={ability}>{ability}</span>)}
                  </div>
                </section>
              </aside>
            </div>

            <section className="chosen-project-evidence">
              <div className="chosen-project-section-heading">
                <span className="eyebrow">选择依据</span>
                <h3>已经知道什么，还需要核对什么</h3>
              </div>
              <div className="evidence-boundaries">
                <div>
                  <span>画像中的已知条件</span>
                  {chosenProject.knownFacts.map((fact) => <p key={fact}>{fact}</p>)}
                </div>
                <div className="is-assumption">
                  <span>开始前仍需验证</span>
                  {chosenProject.assumptions.map((assumption) => <p key={assumption}>{assumption}</p>)}
                </div>
              </div>
            </section>

            <footer className="chosen-project-footer">
              <p>首版保存项目选择和推荐依据，暂不生成开发计划或代码。</p>
              <button className="button" type="button" onClick={() => { setShowChosenProject(false); setNotice('') }}>
                查看其他推荐
              </button>
            </footer>
          </section>
        </>
      ) : !showResults ? (
        <>
          <section className="result-readiness" aria-labelledby="project-readiness-title">
            <div className="readiness-summary">
              <span className="readiness-lock" aria-hidden="true"><LockKeyhole size={20} /></span>
              <div>
                <span className="eyebrow">生成条件</span>
                <h2 id="project-readiness-title">完成两类画像后再生成推荐</h2>
                <p>当前还需补充 2 份相关 JD，并完成 3 项岗位能力评估。</p>
              </div>
            </div>

            <div className="readiness-requirements">
              <article>
                <div className="requirement-heading">
                  <span className="requirement-icon" aria-hidden="true"><BriefcaseBusiness size={17} /></span>
                  <div><strong>市场画像</strong><span>相关且处理完成的 JD</span></div>
                  <b>8 / 10</b>
                </div>
                <div className="requirement-meter" aria-hidden="true"><span style={{ width: '80%' }} /></div>
                <p>达到门槛还需要 2 份符合当前目标的完整 JD。</p>
                <button className="button" type="button" onClick={onOpenMarket}>补充 JD <ArrowRight size={14} /></button>
              </article>

              <article>
                <div className="requirement-heading">
                  <span className="requirement-icon" aria-hidden="true"><UserRound size={17} /></span>
                  <div><strong>用户画像</strong><span>当前目标所需能力</span></div>
                  <b>13 / 16</b>
                </div>
                <div className="requirement-meter" aria-hidden="true"><span style={{ width: '81.25%' }} /></div>
                <p>Redis、Agent 开发和大模型基础仍待评估。</p>
                <button className="button" type="button" onClick={onOpenProfile}>补齐能力 <ArrowRight size={14} /></button>
              </article>
            </div>
          </section>

          <section className="project-result-empty" aria-labelledby="project-result-title">
            <div className="project-result-head">
              <div><h2 id="project-result-title">推荐结果</h2><p>每轮最多给出 3 个合格项目；没有合适项目时可以少给。</p></div>
              <span>尚未生成</span>
            </div>
          </section>
        </>
      ) : (
        <>
          <div className="prototype-data-note">
            <span>结果页原型</span>
            <p>以下项目只用于确认页面结构和交互，不是根据当前画像生成的真实推荐。</p>
          </div>

          <section className="recommendation-overview" aria-labelledby="recommendation-result-title">
            <div className="recommendation-result-heading">
              <div>
                <span className="eyebrow">当前推荐</span>
                <h2 id="recommendation-result-title">为你筛选出 3 个方向</h2>
                <p>按求职相关性、个人可承担范围和可展示性排列。</p>
              </div>
              <span>后端开发＋Agent 应用</span>
            </div>

            <div className="project-card-grid">
              {recommendations.map((project) => (
                <article className="recommendation-card" key={project.id}>
                  <div className="recommendation-card-top">
                    <span className="project-rank">#{project.rank}</span>
                    <div className="recommendation-card-badges">
                      {chosenProjectId === project.id && <span className="project-chosen-badge">已选择</span>}
                      <span className={`project-type ${project.type === '开源二开' ? 'is-extension' : ''}`}>{project.type}</span>
                    </div>
                  </div>
                  <h3>{project.title}</h3>
                  <p className="project-summary">{project.summary}</p>
                  <div className="card-divider" />
                  <span className="card-section-label">为什么适合你</span>
                  <ul>
                    {project.fitReasons.map((reason) => <li key={reason}>{reason}</li>)}
                  </ul>
                  <div className="project-skill-tags">
                    {project.abilities.slice(0, 3).map((ability) => <span key={ability}>{ability}</span>)}
                  </div>
                  <div className="recommendation-card-footer">
                    <span><Clock3 size={13} aria-hidden="true" />{project.duration}</span>
                    <button
                      type="button"
                      onClick={() => {
                        if (chosenProjectId === project.id) setShowChosenProject(true)
                        else setSelectedProjectId(project.id)
                      }}
                    >
                      {chosenProjectId === project.id ? '查看已选项目' : '查看详情'} <ArrowRight size={14} />
                    </button>
                  </div>
                </article>
              ))}
            </div>
          </section>
        </>
      )}

      {drawerOpen && (
        <div className="drawer-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) closeDrawer() }}>
          <aside className="market-drawer project-detail-drawer" role="dialog" aria-modal="true" aria-labelledby="project-drawer-title">
            <header className="drawer-header">
              <div>
                <span>{showRegenerate ? '本次推荐条件' : selectedProject?.type}</span>
                <h2 id="project-drawer-title">{showRegenerate ? '调整后重新推荐' : selectedProject?.title}</h2>
              </div>
              <button className="icon-button" type="button" onClick={closeDrawer} aria-label="关闭抽屉"><X size={19} /></button>
            </header>

            {showRegenerate && (
              <div className="drawer-body regenerate-form">
                <div className="drawer-intro">
                  <RotateCcw size={17} aria-hidden="true" />
                  <p>调整意见只影响本次推荐，不会自动修改用户画像。新结果生成成功前，页面继续保留当前结果。</p>
                </div>
                <label htmlFor="project-adjustment">调整意见（选填）</label>
                <textarea id="project-adjustment" value={adjustment} onChange={(event) => setAdjustment(event.target.value)} placeholder="例如：项目太复杂；想换一个更偏后端工程的场景……" autoFocus />
                <div className="adjustment-suggestions">
                  {['项目太复杂', '换个应用场景', '更偏后端工程'].map((suggestion) => (
                    <button type="button" key={suggestion} onClick={() => setAdjustment(suggestion)}>{suggestion}</button>
                  ))}
                </div>
                <div className="drawer-actions">
                  <button className="button" type="button" onClick={closeDrawer}>取消</button>
                  <button className="button button-primary" type="button" onClick={submitRegenerate}>重新推荐</button>
                </div>
              </div>
            )}

            {selectedProject && (
              <div className="drawer-body project-detail-body">
                <div className="project-detail-summary">
                  <div><span>推荐顺序</span><strong>#{selectedProject.rank}</strong></div>
                  <div><span>项目类型</span><strong>{selectedProject.type}</strong></div>
                  <div><span>预计范围</span><strong>{selectedProject.duration}</strong></div>
                </div>

                <section>
                  <h3>项目是什么</h3>
                  <dl className="project-definition-list">
                    <div><dt>面向谁</dt><dd>{selectedProject.audience}</dd></div>
                    <div><dt>解决什么</dt><dd>{selectedProject.problem}</dd></div>
                    <div><dt>大致做成什么</dt><dd>{selectedProject.shape}</dd></div>
                  </dl>
                </section>

                <section>
                  <h3>为什么推荐给你</h3>
                  <ul className="project-detail-list">
                    {selectedProject.fitReasons.map((reason) => <li key={reason}>{reason}</li>)}
                  </ul>
                </section>

                <section>
                  <h3>你需要完成的主体工作</h3>
                  <ol className="project-scope-list">
                    {selectedProject.scope.map((item) => <li key={item}>{item}</li>)}
                  </ol>
                </section>

                <section>
                  <h3>能够体现的能力</h3>
                  <div className="project-skill-tags is-detail">
                    {selectedProject.abilities.map((ability) => <span key={ability}>{ability}</span>)}
                  </div>
                </section>

                <section>
                  <h3>依据边界</h3>
                  <div className="evidence-boundaries">
                    <div>
                      <span>画像中的已知条件</span>
                      {selectedProject.knownFacts.map((fact) => <p key={fact}>{fact}</p>)}
                    </div>
                    <div className="is-assumption">
                      <span>开始前仍需验证</span>
                      {selectedProject.assumptions.map((assumption) => <p key={assumption}>{assumption}</p>)}
                    </div>
                  </div>
                </section>

                <div className="project-selection-actions">
                  <button className="button" type="button" onClick={closeDrawer}>继续比较</button>
                  <button className="button button-primary" type="button" onClick={chooseProject}>
                    <CheckCircle2 size={16} aria-hidden="true" />
                    选择这个项目
                  </button>
                </div>
              </div>
            )}
          </aside>
        </div>
      )}
    </div>
  )
}
