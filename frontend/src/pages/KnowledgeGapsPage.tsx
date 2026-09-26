import { useEffect, useMemo, useState } from 'react'
import { ArrowLeft, ArrowRight, BookOpenCheck, CheckCircle2, ChevronRight, ClipboardCheck, GraduationCap, LockKeyhole, MessageSquareText, Play, RefreshCw, Send, UserRound, X } from 'lucide-react'
import { jobPilotAPI, type KnowledgeGapItem, type KnowledgeGapView, type ProfileCapability, type ProfilePracticeSession } from '../api'

interface Props { onOpenMarket: () => void; onOpenProfile: () => void; onOpenSettings: () => void }
type Section = 'overview' | 'interview'
type Mode = 'validation' | 'review'

function message(error: unknown) { return error instanceof Error ? error.message : '操作失败，请稍后重试' }
function sourceLabel(source: string) { return source === 'explicit' ? 'JD 明确要求' : '根据职责推断' }
function userSource(source?: string) { return ({manual:'用户自行设置',initial:'初始评估确认',validation:'能力验证确认',evidence:'简历与经历证据'} as Record<string,string>)[source ?? ''] ?? '尚未评估' }

export function KnowledgeGapsPage({ onOpenMarket, onOpenProfile, onOpenSettings }: Props) {
  const [section, setSection] = useState<Section>('overview')
  const [view, setView] = useState<KnowledgeGapView | null>(null)
  const [abilities, setAbilities] = useState<ProfileCapability[]>([])
  const [sessions, setSessions] = useState<ProfilePracticeSession[]>([])
  const [selected, setSelected] = useState<KnowledgeGapItem | null>(null)
  const [mode, setMode] = useState<Mode>('validation')
  const [abilityID, setAbilityID] = useState('')
  const [session, setSession] = useState<ProfilePracticeSession | null>(null)
  const [draft, setDraft] = useState('')
  const [hint, setHint] = useState(false)
  const [reference, setReference] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [modelConfigured, setModelConfigured] = useState<boolean | null>(null)

  const reload = async () => {
    const [gaps, profile, history] = await Promise.all([jobPilotAPI.getKnowledgeGaps(), jobPilotAPI.getUserProfile(), jobPilotAPI.listProfileSessions()])
    setView(gaps); setAbilities(profile.profile.capabilities ?? []); setSessions(history.sessions ?? [])
  }
  useEffect(() => { let active = true; setLoading(true); reload().catch((e) => { if (active) setError(message(e)) }).finally(() => { if (active) setLoading(false) }); return () => { active = false } }, [])
  useEffect(() => { jobPilotAPI.getDeepSeekConfig().then((config) => setModelConfigured(config.configured)).catch(() => {}) }, [])

  const report = view?.report
  const selectedAbility = useMemo(() => abilities.find((item) => item.concept_id === abilityID), [abilities, abilityID])
  const eligible = useMemo(() => abilities.filter((item) => item.levels.length === 6 && (mode === 'review' || item.assessed && item.current_level < 5)), [abilities, mode])
  const activeAbility = selectedAbility && eligible.some((item) => item.concept_id === selectedAbility.concept_id) ? selectedAbility : eligible[0]
  const resumable = sessions.find((item) => item.concept_id === activeAbility?.concept_id && item.mode === mode && item.status !== 'evaluated')
  const answered = new Map((session?.answers ?? []).map((item) => [item.question_id, item.answer]))
  const currentQuestion = session?.questions.find((item) => !answered.has(item.id))
  const questionNumber = currentQuestion ? session?.questions.findIndex((item) => item.id === currentQuestion.id) ?? 0 : session?.questions.length ?? 0

  const analyze = async () => {
    setBusy(true); setError(''); setNotice('')
    try { const result = await jobPilotAPI.analyzeKnowledgeGaps(); setView(result); setNotice('短板报告已根据当前画像更新。') }
    catch (e) { setError(message(e)); await jobPilotAPI.getKnowledgeGaps().then(setView).catch(() => {}) }
    finally { setBusy(false) }
  }
  const start = async () => {
    if (!activeAbility) return
    setBusy(true); setError('')
    try { const result = await jobPilotAPI.startProfileSession(activeAbility.concept_id, mode); setSession(result.session); setDraft(''); await reload() }
    catch (e) { setError(message(e)) } finally { setBusy(false) }
  }
  const resume = () => { if (resumable) { setSession(resumable); setDraft('') } }
  const answer = async () => {
    if (!session || !currentQuestion || !draft.trim()) return
    setBusy(true); setError('')
    try {
      const saved = await jobPilotAPI.saveProfileAnswer(session.id, currentQuestion.id, draft.trim())
      let next = saved.session
      if (next.answers?.length === next.questions.length) {
        const result = await jobPilotAPI.submitProfileSession(next.id, next.answers)
        next = result.session
        await reload()
      }
      setSession(next); setDraft(''); setHint(false); setReference(false)
    } catch (e) { setError(message(e)) } finally { setBusy(false) }
  }
  const retryEvaluation = async () => {
    if (!session?.answers || session.answers.length !== session.questions.length) return
    setBusy(true); setError('')
    try { const result = await jobPilotAPI.submitProfileSession(session.id, session.answers); setSession(result.session); await reload() }
    catch (e) { setError(message(e)) } finally { setBusy(false) }
  }
  const confirm = async () => {
    if (!session) return
    setBusy(true); setError('')
    try { const result = await jobPilotAPI.confirmProfileSession(session.id); setSession(result.session); setNotice(`已确认提升至 L${result.session.target_level}。`); await reload() }
    catch (e) { setError(message(e)) } finally { setBusy(false) }
  }

  return <div className="page-content gaps-page">
    <div className="page-heading-row"><div><h1>知识短板</h1><p className="page-description">{section === 'overview' ? '对照市场画像和用户画像，查看完整能力的等级差距。' : '按能力等级标准完成验证，或用真实问题复习。'}</p></div>{section === 'overview' && <button className="button button-primary gaps-analyze-button" type="button" disabled={busy || loading || view?.readiness.code !== 'ready'} onClick={analyze}><RefreshCw size={15} />{busy ? '分析中……' : report ? '重新分析' : '分析短板'}</button>}</div>
    <div className="knowledge-section-tabs" role="tablist" aria-label="知识短板页面"><button className={section === 'overview' ? 'is-active' : ''} type="button" role="tab" aria-selected={section === 'overview'} onClick={() => setSection('overview')}>短板总览</button><button className={section === 'interview' ? 'is-active' : ''} type="button" role="tab" aria-selected={section === 'interview'} onClick={() => setSection('interview')}>面试问答</button></div>
    {notice && <div className="market-notice" role="status"><CheckCircle2 size={17} /><span>{notice}</span><button type="button" onClick={() => setNotice('')} aria-label="关闭提示"><X size={15} /></button></div>}
    {error && (section !== 'interview' || session) && <div className="notice" role="alert">{error}<button type="button" onClick={() => setError('')} aria-label="关闭错误">×</button></div>}
    {loading && <section className="gap-result-empty"><p>正在读取画像与报告……</p></section>}

    {!loading && section === 'overview' && <>
      {view?.readiness.code !== 'ready' && <section className="result-readiness"><div className="readiness-summary"><span className="readiness-lock"><LockKeyhole size={20} /></span><div><span className="eyebrow">分析条件</span><h2>{view?.readiness.message || '画像尚未准备完成'}</h2><p>旧报告仍可查看；准备完成后由你重新分析。</p></div></div><div className="readiness-requirements"><article><div className="requirement-heading"><BookOpenCheck size={17} /><div><strong>市场画像</strong><span>至少 10 份计入的 JD，完成审核与判级</span></div><b>{view?.readiness.included_jd_count ?? 0} / 10</b></div><p>待审核 {view?.readiness.pending_review_count ?? 0} 项，审核失败 {view?.readiness.failed_review_count ?? 0} 项；待判级 {view?.readiness.pending_grading_count ?? 0} 项，判级失败 {view?.readiness.failed_grading_count ?? 0} 项。</p><button className="button" type="button" onClick={onOpenMarket}>查看市场画像 <ArrowRight size={14} /></button></article><article><div className="requirement-heading"><UserRound size={17} /><div><strong>用户画像</strong><span>确认必要信息与能力等级</span></div><b>{view?.readiness.missing_ability_count ?? 0} 项待判断</b></div><p>任选或多选要求只需评估到足以判断整条要求，不会把所有候选都当作必备。</p><button className="button" type="button" onClick={onOpenProfile}>补齐画像 <ArrowRight size={14} /></button></article></div></section>}
      {!report ? <section className="gap-result-empty"><div className="project-result-head"><div><h2>短板结果</h2><p>完成两类画像后点击“分析短板”，结果只比较完整能力项。</p></div><span>尚未分析</span></div></section> : <>
        {view?.stale && <div className="market-notice" role="status"><RefreshCw size={17} /><span>画像资料已有变化，下面保留上次报告。准备完成后请重新分析。</span></div>}
        <section className="gap-summary-panel"><div className="gap-summary-copy"><span className="eyebrow">{view?.stale ? '上次结果 · 待更新' : '当前结果'}</span><h2>{report.gaps.length} 项要求需要补强</h2><p>仅判断完整能力和整条组合要求，不推测 MySQL 等能力内部的薄弱知识点。</p></div><div className="gap-summary-counts"><div className="is-gap"><strong>{report.gaps.length}</strong><span>需要补强</span></div><div><strong>{report.met.length}</strong><span>已经达到</span></div><div><strong>{report.preferred.length}</strong><span>加分准备</span></div></div></section>
        <section className="gap-result-panel"><div className="gap-result-heading"><div><h2>需要补强</h2><p>点击查看用户等级、JD 原文及判级理由。</p></div><span>{report.gaps.length} 项</span></div><div className="gap-table-head" aria-hidden="true"><span>能力或要求</span><span>用户当前</span><span>目标等级</span><span>差距</span><span>判断依据</span><span /></div><div className="gap-list">{report.gaps.length === 0 ? <p className="gap-boundary-note">当前已评估的必需要求均已达到。</p> : report.gaps.map((item) => <button className="gap-row" type="button" key={item.id} onClick={() => setSelected(item)}><strong>{item.kind === 'ability' ? item.name : item.options?.map((o) => o.name).join(' / ') || item.name}</strong><span><b className="level-chip">{item.kind === 'ability' ? `L${item.current_level}` : `${item.satisfied_count ?? 0} 项`}</b></span><span><b className="target-level">{item.kind === 'ability' ? `L${item.target_level}` : `需 ${item.required_count} 项`}</b></span><span className="gap-distance">{item.kind === 'ability' ? `差 ${Math.max(0, (item.target_level ?? 0) - (item.current_level ?? 0))} 级` : '组合未满足'}</span><span className="gap-source">{item.sample_count} 份 JD</span><ChevronRight size={16} /></button>)}</div></section>
        {(report.met.length > 0 || report.preferred.length > 0) && <section className="gap-result-panel"><div className="gap-result-heading"><div><h2>已达标与加分准备</h2><p>加分要求单独列出，不混入必备短板。</p></div></div><div className="gap-list">{[...report.met, ...report.preferred].map((item) => <button className="gap-row" type="button" key={item.id} onClick={() => setSelected(item)}><strong>{item.kind === 'ability' ? item.name : item.options?.map((o) => o.name).join(' / ') || item.name}</strong><span>{report.preferred.some((p) => p.id === item.id) ? '加分项' : '已达标'}</span><span>{item.kind === 'ability' ? `L${item.current_level} / L${item.target_level}` : `${item.satisfied_count} / ${item.required_count}`}</span><span /><span>{item.sample_count} 份 JD</span><ChevronRight size={16} /></button>)}</div></section>}
      </>}
    </>}

    {!loading && section === 'interview' && <>{!session ? <div className="interview-setup"><section className="interview-mode-section"><div className="interview-section-heading"><div><span className="eyebrow">第一步</span><h2>选择问答方式</h2><p>验证需要用户确认才升级；复习永远不改等级。</p></div></div><div className="interview-mode-grid"><button className={`interview-mode-card ${mode === 'validation' ? 'is-selected' : ''}`} type="button" onClick={() => setMode('validation')}><span className="interview-mode-icon"><ClipboardCheck size={20} /></span><span className="interview-mode-copy"><strong>能力验证</strong><small>6 道核心题，最多 3 道澄清追问。</small></span><span className="interview-mode-rule">只建议升一级</span></button><button className={`interview-mode-card ${mode === 'review' ? 'is-selected' : ''}`} type="button" onClick={() => setMode('review')}><span className="interview-mode-icon"><GraduationCap size={20} /></span><span className="interview-mode-copy"><strong>复习练习</strong><small>可查看提示、参考答案与讲解。</small></span><span className="interview-mode-rule">等级保持不变</span></button></div></section><section className="interview-ability-section"><div className="interview-section-heading"><div><span className="eyebrow">第二步</span><h2>选择完整能力</h2><p>题目根据当前能力的 L0–L5 标准生成。</p></div></div><div className="interview-ability-list">{eligible.map((item) => <button className={`interview-ability-row ${activeAbility?.concept_id === item.concept_id ? 'is-selected' : ''}`} type="button" key={item.concept_id} onClick={() => setAbilityID(item.concept_id)}><strong>{item.name}</strong><span><b className="level-chip">{item.assessed ? `L${item.current_level}` : '未评估'}</b></span><span className="interview-target-level"><b>{mode === 'validation' ? `L${item.current_level + 1}` : item.market_level_ready ? `L${item.market_level}` : '复习'}</b></span><span className="gap-source">{userSource(item.level_source)}</span><span className="interview-select-mark">{activeAbility?.concept_id === item.concept_id ? '已选择' : '选择'}</span></button>)}</div>{modelConfigured === false && <div className="interview-model-alert" role="alert"><span>此账号尚未配置 DeepSeek API Key，无法生成问答。</span><button className="button" type="button" onClick={onOpenSettings}>前往设置</button></div>}<div className="interview-start-bar"><div><span>已选择</span><strong>{activeAbility?.name ?? '暂无可用能力'}</strong><small>{resumable ? '存在未完成的会话，可继续作答。' : '生成问题后会保存会话，刷新页面可继续。'}</small></div>{resumable && <button className="button" type="button" onClick={resume}>继续上次问答</button>}<button className="button button-primary" type="button" disabled={!activeAbility || busy || modelConfigured === false} onClick={start}><Play size={15} />{modelConfigured === false ? '配置模型后可新建' : busy ? '生成中……' : '新建问答'}</button></div>{error && <div className="interview-model-alert is-error" role="alert"><span>{error}</span><button type="button" onClick={() => setError('')} aria-label="关闭错误"><X size={15} /></button></div>}</section></div> : <div className="interview-session"><section className="interview-session-head"><button className="interview-back-button" type="button" onClick={() => setSession(null)}><ArrowLeft size={16} />返回选择</button><div><span className="interview-session-mode">{session.mode === 'review' ? '复习练习' : '能力验证'}</span><h2>{session.concept_name}</h2><p>{session.mode === 'review' ? '练习不会改变用户能力等级' : `当前 L${session.base_level}，本轮只验证 L${session.target_level}`}</p></div></section><div className="interview-workspace"><section className="interview-question-panel">{session.status === 'evaluated' && session.evaluation ? <div className="interview-complete-state"><span className="interview-complete-icon"><CheckCircle2 size={24} /></span><span className="eyebrow">本轮完成</span><h3>{session.mode === 'review' ? '复习反馈' : session.evaluation.verdict === 'pass' ? '模型建议升级' : session.evaluation.verdict === 'uncertain' ? '目前证据不足' : '暂未达到下一级'}</h3><p>{session.evaluation.summary}</p>{session.evaluation.question_results.map((result) => <div className="profile-question-result" key={result.question_id}><strong>{result.question_id}</strong><p>{result.feedback}</p></div>)}{session.mode === 'validation' && session.evaluation.verdict === 'pass' && !session.level_updated && <button className="button button-primary" type="button" disabled={busy} onClick={confirm}>确认提升至 L{session.target_level}</button>}{session.level_updated && <p>已确认，用户画像更新为 L{session.target_level}。</p>}</div> : currentQuestion ? <><div className="interview-progress-row"><div><span>{session.status === 'clarifying' && questionNumber >= 6 ? '澄清追问' : '核心题进度'}</span><strong>{questionNumber + 1} / {session.questions.length}</strong></div><span>追问 {session.clarification_count} / 3</span></div><div className="interview-agent-question"><div className="interview-question-label"><span><MessageSquareText size={15} />Agent 提问</span><b>第 {questionNumber + 1} 题</b></div><h3>{currentQuestion.prompt}</h3><p>{currentQuestion.dimension}</p></div>{session.mode === 'review' && <div className="review-aids"><button type="button" onClick={() => setHint(!hint)}>{hint ? '收起提示' : '查看提示'}</button><button type="button" onClick={() => setReference(!reference)}>{reference ? '收起参考答案' : '查看参考答案'}</button></div>}{hint && session.mode === 'review' && <div className="interview-aid-box"><span>提示</span><p>{currentQuestion.hint || '本题暂无提示'}</p></div>}{reference && session.mode === 'review' && <div className="interview-aid-box is-reference"><span>参考答案与讲解</span><p>{currentQuestion.reference || '本题暂无参考答案'}</p><p>{currentQuestion.explanation}</p></div>}<div className="interview-answer-area"><label htmlFor="interview-answer">你的回答</label><textarea id="interview-answer" value={draft} onChange={(event) => setDraft(event.target.value)} placeholder="结合原理、场景和你的判断过程作答……" /><div className="interview-answer-actions"><span>每题提交后立即保存，评估失败可重试。</span><button className="button button-primary" type="button" disabled={!draft.trim() || busy} onClick={answer}>提交并继续 <Send size={15} /></button></div></div></> : <div className="interview-complete-state"><h3>回答已保存，等待模型评估</h3><p>如上次调用失败，可重试；不会重复提交等级结果。</p><button className="button button-primary" type="button" disabled={busy} onClick={retryEvaluation}>重试评估</button></div>}</section><aside className="interview-session-aside"><section><span className="eyebrow">本轮规则</span><h3>{session.mode === 'review' ? '仅用于复习' : '逐级验证'}</h3><ul><li>6 道核心题，验证最多 3 道追问</li><li>模型综合判断并说明理由</li><li>升级须由你确认，未通过不降级</li></ul></section></aside></div></div>}</>}

    {section === 'overview' && selected && <div className="drawer-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setSelected(null) }}><aside className="market-drawer gap-detail-drawer" role="dialog" aria-modal="true" aria-labelledby="gap-drawer-title"><header className="drawer-header"><div><span>真实画像依据</span><h2 id="gap-drawer-title">{selected.kind === 'ability' ? selected.name : selected.options?.map((o) => o.name).join(' / ')}</h2></div><button className="icon-button" type="button" onClick={() => setSelected(null)} aria-label="关闭抽屉"><X size={19} /></button></header><div className="drawer-body gap-detail-body">{selected.kind === 'ability' ? <><div className="gap-level-comparison"><div><span>用户当前等级</span><strong>L{selected.current_level}</strong></div><ArrowRight size={18} /><div><span>市场常见等级</span><strong>L{selected.target_level}</strong></div></div><div className="gap-boundary-note">只比较 {selected.name} 的整体等级；用户等级来源：{userSource(selected.user_source)}。不推测内部具体薄弱知识点。</div><section><h3>用户能力依据</h3><div className="gap-evidence-list">{selected.user_evidence?.length ? selected.user_evidence.map((item) => <article key={item.id}><span>材料证据 · 支持 L{item.level}</span><p>“{item.quote}”</p><p>{item.reason}</p></article>) : <p>当前等级由用户设置或问答确认，没有材料原文证据。</p>}</div></section></> : <><div className="gap-boundary-note">这是一条完整的组合要求，满足其中 {selected.required_count} 项不同能力即可。</div><section><h3>可满足的能力</h3>{selected.options?.map((option) => <p key={option.ability_id}>{option.name}：{option.assessed ? `用户 L${option.current_level}` : '未评估'}，该 JD 要求 L{option.required_level}，{option.satisfied ? '已满足' : '尚未满足'}</p>)}</section></>}<section><h3>JD 原文与判级理由</h3><div className="gap-evidence-list is-market">{selected.evidences.map((item, index) => <article key={`${item.jd_id}-${index}`}><span>{item.jd_title} · L{item.level} · {sourceLabel(item.source)}</span><p>“{item.quote}”</p><p>{item.reason}</p></article>)}</div></section><section className="gap-conclusion"><span>样本范围</span><p>基于 {selected.sample_count} 份计入市场画像的 JD；同一能力在同一 JD 中只计一次。</p></section></div></aside></div>}
  </div>
}
