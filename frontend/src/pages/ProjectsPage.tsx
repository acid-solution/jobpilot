import { useEffect, useMemo, useState } from 'react'
import { ArrowLeft, ArrowRight, BriefcaseBusiness, CheckCircle2, Clock3, LockKeyhole, RotateCcw, Sparkles, UserRound, X } from 'lucide-react'
import { jobPilotAPI, type ProjectRecommendationsView, type RecommendedProject } from '../api'

interface Props { onOpenMarket: () => void; onOpenProfile: () => void }
const message = (error: unknown) => error instanceof Error ? error.message : '操作失败，请稍后重试'
const active = (status?: string) => status === 'queued' || status === 'running'
function jobError(code?: string) {
  switch (code) {
    case 'github_unavailable': return 'GitHub 暂时无法检索，旧推荐已保留，可以重试。'
    case 'model_not_configured': return '请先在设置中配置 DeepSeek API Key。'
    case 'inputs_changed': return '生成期间画像发生变化，请根据当前画像重新推荐。'
    case 'model_invalid_response': return '模型返回的项目内容不完整，可以重试。'
    default: return '项目推荐暂时失败，旧推荐已保留，可以重试。'
  }
}
function retryReason(code?: string) {
  switch (code) {
    case 'github_unavailable': return '上次 GitHub 检索暂时失败'
    case 'model_invalid_response': return '上次模型结果需要重新生成'
    case 'worker_interrupted': return '上次处理被中断'
    default: return '上次处理未完成'
  }
}
function RecommendationProgress({ job, hasReport }: { job: NonNullable<ProjectRecommendationsView['job']>; hasReport: boolean }) {
  const phases = [
    { key: 'draft', title: '形成项目设想', detail: '结合两类画像生成候选项目' },
    { key: 'research', title: '核查 GitHub 参考', detail: job.draft_count ? `已核查 ${job.researched_count} / ${job.draft_count} 个候选项目` : '逐个核查参考仓库' },
    { key: 'compare', title: '比较并筛选', detail: '核对差异、投入范围和推荐理由' },
  ] as const
  const current = Math.max(0, phases.findIndex((phase) => phase.key === job.phase))
  const retrying = job.status === 'queued' && job.attempts > 0 && Boolean(job.error_code)
  const nextAttempt = retrying && job.next_attempt_at ? new Date(job.next_attempt_at) : null
  const nextTime = nextAttempt && !Number.isNaN(nextAttempt.getTime()) && nextAttempt.getTime() > Date.now()
    ? nextAttempt.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', timeZone: 'Asia/Shanghai' })
    : null

  return <section className="recommendation-progress" aria-label="推荐任务进度" role="status">
    <div className="recommendation-progress-head">
      <div><span className="eyebrow">推荐进度</span><h2>{retrying ? '等待自动重试' : job.status === 'queued' ? '正在排队' : `第 ${current + 1} / 3 阶段：${phases[current].title}`}</h2>
        <p>{retrying ? `${retryReason(job.error_code)}，已尝试 ${job.attempts} / ${job.max_attempts} 次。${nextTime ? `不早于今天 ${nextTime} 再次尝试。` : '系统会自动继续。'}` : job.status === 'queued' ? '任务已创建，等待后台领取。' : current === 1 ? phases[1].detail : phases[current].detail} {hasReport ? '旧推荐会保留到新结果完成。' : ''}</p></div>
      <span className={`recommendation-progress-state ${retrying ? 'is-retrying' : ''}`}>{retrying ? '待重试' : job.status === 'queued' ? '排队中' : '进行中'}</span>
    </div>
    <ol className="recommendation-progress-steps">
      {phases.map((phase, index) => {
        const done = index < current
        const ongoing = index === current
        return <li className={done ? 'is-done' : ongoing ? 'is-current' : ''} key={phase.key}>
          <span className="recommendation-progress-marker" aria-hidden="true">{done ? <CheckCircle2 size={17} /> : index + 1}</span>
          <div><strong>{phase.title}</strong><small>{index === 1 && job.draft_count > 0 ? phase.detail : done ? '已完成' : ongoing ? job.status === 'queued' ? '等待继续' : phase.detail : '尚未开始'}</small></div>
        </li>
      })}
    </ol>
    <p className="recommendation-progress-note">步骤表示已完成的处理阶段，不代表耗时比例；模型生成和 GitHub 检索用时可能不同。</p>
  </section>
}
function List({ items }: { items?: string[] }) { return <ul className="project-detail-list">{(items ?? []).map((item, index) => <li key={`${index}:${item}`}>{item}</li>)}</ul> }

function References({ project }: { project: RecommendedProject }) {
  return <section className="chosen-project-evidence">
    <div className="chosen-project-section-heading"><span className="eyebrow">参考资料</span><h3>与 GitHub 项目的差异和取舍</h3></div>
    {project.references.length === 0 ? <p>{project.no_reference_reason || '暂未找到合适的 GitHub 参考项目。'}</p> : project.references.map((repo) => <article className="project-reference" key={repo.full_name}>
      <div className="project-reference-title"><a href={repo.url} target="_blank" rel="noopener noreferrer">{repo.full_name} ↗</a><span>{repo.language || '语言未标注'}{repo.archived ? ' · 已归档' : ''}</span></div>
      {repo.description && <p>{repo.description}</p>}
      <div className="project-reference-grid">
        <div><strong>相同点</strong><List items={repo.comparison.similarities} /></div>
        <div><strong>关键差异</strong><List items={repo.comparison.differences} /></div>
        <div><strong>推荐方案的优势与代价</strong><List items={repo.comparison.project_advantages} /><List items={repo.comparison.project_limits} /></div>
        <div><strong>参考仓库的优势与代价</strong><List items={repo.comparison.repository_advantages} /><List items={repo.comparison.repository_limits} /></div>
      </div>
      <details className="project-reference-facts"><summary>查看已核实的仓库依据</summary>{repo.facts.map((fact, index) => <p key={index}>{fact.text} <a href={fact.source_url} target="_blank" rel="noopener noreferrer">查看来源 ↗</a></p>)}</details>
    </article>)}
    {project.references.length === 0 && project.searched_directions.length > 0 && <p className="detail-muted">已检索：{project.searched_directions.join('、')}</p>}
  </section>
}

export function ProjectsPage({ onOpenMarket, onOpenProfile }: Props) {
  const [view, setView] = useState<ProjectRecommendationsView | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [selectedID, setSelectedID] = useState<string | null>(null)
  const [showChosen, setShowChosen] = useState(false)
  const [showRegenerate, setShowRegenerate] = useState(false)
  const [adjustment, setAdjustment] = useState('')

  useEffect(() => { let mounted = true; jobPilotAPI.getProjectRecommendations().then((result) => { if (mounted) setView(result) }).catch((e) => { if (mounted) setError(message(e)) }).finally(() => { if (mounted) setLoading(false) }); return () => { mounted = false } }, [])
  useEffect(() => { if (!active(view?.job?.status)) return; const interval = window.setInterval(() => { jobPilotAPI.getProjectRecommendations().then(setView).catch((e) => setError(message(e))) }, 5000); return () => window.clearInterval(interval) }, [view?.job?.status])
  const projects = view?.report?.projects ?? []
  useEffect(() => { setSelectedID(null); setShowRegenerate(false); setShowChosen(false) }, [view?.report?.id])
  const selected = useMemo(() => projects.find((item) => item.id === selectedID) ?? null, [projects, selectedID])
  const chosen = useMemo(() => projects.find((item) => item.id === view?.selected_project_id) ?? null, [projects, view?.selected_project_id])
  const ready = view?.readiness.code === 'ready'
  const running = active(view?.job?.status)
  const drawerOpen = Boolean(selected || showRegenerate)
  const closeDrawer = () => { setSelectedID(null); setShowRegenerate(false); setAdjustment('') }
  useEffect(() => { if (!drawerOpen) return; const onEscape = (event: KeyboardEvent) => { if (event.key === 'Escape') closeDrawer() }; window.addEventListener('keydown', onEscape); return () => window.removeEventListener('keydown', onEscape) }, [drawerOpen])

  const generate = async (opinion = '') => {
    setBusy(true); setError(''); setNotice('')
    try { const result = await jobPilotAPI.generateProjectRecommendations(opinion); setView(result); closeDrawer(); setShowChosen(false); setNotice('推荐任务已开始。新结果完成前会保留当前推荐。') }
    catch (e) { setError(message(e)); await jobPilotAPI.getProjectRecommendations().then(setView).catch(() => {}) }
    finally { setBusy(false) }
  }
  const choose = async () => {
    if (!selected || !view?.report) return
    setBusy(true); setError('')
    try { const result = await jobPilotAPI.selectRecommendedProject(view.report.id, selected.id); setView(result); setShowChosen(true); closeDrawer(); setNotice(`已选择“${selected.title}”。`) }
    catch (e) { setError(message(e)) } finally { setBusy(false) }
  }

  return <div className="page-content projects-page">
    <div className="page-heading-row"><div><h1>{showChosen && chosen ? '已选项目' : '项目推荐'}</h1><p className="page-description">{showChosen && chosen ? '查看已经选定的方向、推荐依据和开始前需要核对的事项。' : '结合目标岗位和个人情况，筛选值得投入、适合写进简历的项目。'}</p></div>
      <div className="project-heading-actions">{showChosen && chosen ? <button className="button" type="button" onClick={() => setShowChosen(false)}><ArrowLeft size={15} />返回推荐列表</button> : view?.report ? <button className="button button-primary projects-generate-button" type="button" disabled={!ready || running || busy} onClick={() => setShowRegenerate(true)}><RotateCcw size={15} />调整后重新推荐</button> : <button className="button button-primary projects-generate-button" type="button" disabled={!ready || running || busy || loading} onClick={() => generate()}><Sparkles size={16} />生成推荐</button>}</div>
    </div>
    {error && <div className="market-notice" role="alert"><span>{error}</span><button type="button" onClick={() => setError('')} aria-label="关闭提示"><X size={15} /></button></div>}
    {notice && <div className="market-notice" role="status"><CheckCircle2 size={17} /><span>{notice}</span><button type="button" onClick={() => setNotice('')} aria-label="关闭提示"><X size={15} /></button></div>}
    {loading && <section className="project-result-empty"><p>正在读取当前推荐与画像进度……</p></section>}
    {!loading && view?.job?.status === 'failed' && <div className="project-status-banner" role="alert">{jobError(view.job.error_code)}{ready && <button className="button" type="button" disabled={busy} onClick={() => generate()}>重试推荐</button>}</div>}
    {!loading && running && view?.job && <RecommendationProgress job={view.job} hasReport={Boolean(view.report)} />}
    {!loading && view?.stale && <div className="project-status-banner">画像资料已变化，当前推荐来自旧画像。画像准备完成后可以重新推荐。</div>}
    {!loading && showChosen && chosen && <section className="chosen-project" aria-labelledby="chosen-project-title">
      <div className="chosen-project-hero"><div className="chosen-project-copy"><div className="chosen-project-kicker"><span className="chosen-project-state"><CheckCircle2 size={14} />已选定</span></div><h2 id="chosen-project-title">{chosen.title}</h2><p>{chosen.summary}</p></div><div className="chosen-project-meta"><div><span>对应目标</span><strong>{view?.report?.target_title}</strong></div><div><span>预计投入周期</span><strong>{chosen.duration}</strong></div><div><span>推荐顺序</span><strong>#{chosen.rank}</strong></div></div></div>
      <div className="chosen-project-layout"><div className="chosen-project-main"><section><span className="eyebrow">模型判断的项目设想</span><h3>要解决的问题</h3><p>{chosen.problem}</p></section><section><span className="eyebrow">预期形态</span><h3>准备做成什么</h3><p>{chosen.shape}</p></section><section><span className="eyebrow">个人工作</span><h3>需要完成的主体范围</h3><ol className="chosen-project-scope">{chosen.scope.map((item) => <li key={item}>{item}</li>)}</ol></section></div><aside className="chosen-project-aside"><section><h3>为什么适合你</h3><List items={chosen.fit_reasons} /></section><section><h3>可以体现的能力</h3><div className="project-skill-tags is-detail">{chosen.abilities.map((item) => <span key={item}>{item}</span>)}</div></section></aside></div>
      <section className="chosen-project-evidence"><div className="chosen-project-section-heading"><span className="eyebrow">选择依据</span><h3>已经知道什么，还需要核对什么</h3></div><div className="evidence-boundaries"><div><span>画像中的已知条件</span>{chosen.known_facts.map((item) => <p key={item}>{item}</p>)}</div><div className="is-assumption"><span>开始前仍需验证</span>{chosen.assumptions.map((item) => <p key={item}>{item}</p>)}</div></div></section>
      <References project={chosen} /><footer className="chosen-project-footer"><p>首版保存项目选择和推荐依据，暂不生成开发计划或代码。</p><button className="button" type="button" onClick={() => setShowChosen(false)}>查看其他推荐</button></footer>
    </section>}
    {!loading && !(showChosen && chosen) && !view?.report && <><section className="result-readiness" aria-labelledby="project-readiness-title"><div className="readiness-summary"><span className="readiness-lock"><LockKeyhole size={20} /></span><div><span className="eyebrow">生成条件</span><h2 id="project-readiness-title">完成两类画像后再生成推荐</h2><p>{view?.readiness.message || (ready ? '画像已完成，可以开始生成推荐。' : '正在核对当前画像。')}</p></div></div><div className="readiness-requirements"><article><div className="requirement-heading"><div><strong>市场画像</strong><span>计入当前目标的 JD</span></div><b>{view?.readiness.included_jd_count ?? 0} / 10</b></div><div className="requirement-meter"><span style={{ width: `${Math.min(100, (view?.readiness.included_jd_count ?? 0) * 10)}%` }} /></div><p>能力目录审核与 JD 判级也需要完成。</p><button className="button" type="button" onClick={onOpenMarket}><BriefcaseBusiness size={15} />查看市场画像 <ArrowRight size={14} /></button></article><article><div className="requirement-heading"><div><strong>用户画像</strong><span>必要信息与岗位能力</span></div><b>{(view?.readiness.missing_ability_count ?? 0) === 0 ? '已核对' : `${view?.readiness.missing_ability_count} 项待补`}</b></div><p>结合已有经历和能力等级，判断项目的可完成范围。</p><button className="button" type="button" onClick={onOpenProfile}><UserRound size={15} />查看用户画像 <ArrowRight size={14} /></button></article></div></section><section className="project-result-empty"><div className="project-result-head"><div><h2>推荐结果</h2><p>每轮最多给出 3 个合格项目；没有合适项目时可以少给。</p></div><span>{running ? '生成中' : '尚未生成'}</span></div></section></>}
    {!loading && !(showChosen && chosen) && view?.report && <section className="recommendation-overview" aria-labelledby="recommendation-result-title"><div className="recommendation-result-heading"><div><span className="eyebrow">当前推荐</span><h2 id="recommendation-result-title">{projects.length ? `为你筛选出 ${projects.length} 个方向` : '本轮没有合格项目'}</h2><p>{view.report.empty_reason || '按求职相关性、个人可承担范围和可展示性排列。'}</p></div><span>{view.report.target_title}</span></div><div className="project-card-grid">{projects.map((project) => <article className="recommendation-card" key={project.id}><div className="recommendation-card-top"><span className="project-rank">#{project.rank}</span><div className="recommendation-card-badges">{view.selected_project_id === project.id && <span className="project-chosen-badge">已选择</span>}</div></div><h3>{project.title}</h3><p className="project-summary">{project.summary}</p><div className="card-divider" /><span className="card-section-label">为什么适合你</span><List items={project.fit_reasons} /><div className="project-skill-tags">{project.abilities.slice(0, 3).map((ability) => <span key={ability}>{ability}</span>)}</div><div className="recommendation-card-footer"><span><Clock3 size={13} />{project.duration}</span><button type="button" onClick={() => view.selected_project_id === project.id ? setShowChosen(true) : setSelectedID(project.id)}>{view.selected_project_id === project.id ? '查看已选项目' : '查看详情'} <ArrowRight size={14} /></button></div></article>)}</div></section>}
    {drawerOpen && <div className="drawer-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) closeDrawer() }}><aside className="market-drawer project-detail-drawer" role="dialog" aria-modal="true" aria-labelledby="project-drawer-title"><header className="drawer-header"><div><span>{showRegenerate ? '本次推荐条件' : '项目设想'}</span><h2 id="project-drawer-title">{showRegenerate ? '调整后重新推荐' : selected?.title}</h2></div><button className="icon-button" type="button" onClick={closeDrawer} aria-label="关闭抽屉"><X size={19} /></button></header>
      {showRegenerate && <div className="drawer-body regenerate-form"><div className="drawer-intro"><RotateCcw size={17} /><p>调整意见只影响本次推荐，不会自动修改用户画像。新结果生成成功前，页面继续保留当前结果。</p></div><label htmlFor="project-adjustment">调整意见（选填）</label><textarea id="project-adjustment" value={adjustment} onChange={(event) => setAdjustment(event.target.value)} maxLength={500} placeholder="例如：项目太复杂；想换一个更偏后端工程的场景……" autoFocus /><div className="adjustment-suggestions">{['项目太复杂', '换个应用场景', '更偏后端工程'].map((suggestion) => <button type="button" key={suggestion} onClick={() => setAdjustment(suggestion)}>{suggestion}</button>)}</div><div className="drawer-actions"><button className="button" type="button" onClick={closeDrawer}>取消</button><button className="button button-primary" type="button" disabled={busy} onClick={() => generate(adjustment)}>重新推荐</button></div></div>}
      {selected && <div className="drawer-body project-detail-body"><div className="project-detail-summary"><div><span>推荐顺序</span><strong>#{selected.rank}</strong></div><div><span>参考项目</span><strong>{selected.references.length ? `${selected.references.length} 个` : '暂未找到'}</strong></div><div><span>预计范围</span><strong>{selected.duration}</strong></div></div><section><h3>项目是什么 · 模型判断</h3><dl className="project-definition-list"><div><dt>面向谁</dt><dd>{selected.audience}</dd></div><div><dt>解决什么</dt><dd>{selected.problem}</dd></div><div><dt>大致做成什么</dt><dd>{selected.shape}</dd></div></dl></section><References project={selected} /><section><h3>为什么推荐给你</h3><List items={selected.fit_reasons} /></section><section><h3>你需要完成的主体工作</h3><ol className="project-scope-list">{selected.scope.map((item) => <li key={item}>{item}</li>)}</ol></section><section><h3>能够体现的能力</h3><div className="project-skill-tags is-detail">{selected.abilities.map((ability) => <span key={ability}>{ability}</span>)}</div></section><section><h3>依据边界</h3><div className="evidence-boundaries"><div><span>画像中的已知条件</span>{selected.known_facts.map((item) => <p key={item}>{item}</p>)}</div><div className="is-assumption"><span>开始前仍需验证</span>{selected.assumptions.map((item) => <p key={item}>{item}</p>)}</div></div></section><div className="project-selection-actions"><button className="button" type="button" onClick={closeDrawer}>继续比较</button><button className="button button-primary" type="button" disabled={busy} onClick={choose}><CheckCircle2 size={16} />选择这个项目</button></div></div>}
    </aside></div>}
  </div>
}
