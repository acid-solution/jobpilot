import { useEffect, useMemo, useState, type ChangeEvent } from 'react'
import {
  ArrowRight,
  CheckCircle2,
  ChevronRight,
  FileText,
  LoaderCircle,
  MessageSquareText,
  Pencil,
  Plus,
  RotateCw,
  Trash2,
  Upload,
  X,
} from 'lucide-react'
import {
  APIError,
  jobPilotAPI,
  type JobTarget,
  type ProfileCapability,
  type ProfileMaterial,
  type ProfileMaterialType,
  type ProfilePracticeMode,
  type ProfilePracticeSession,
  type UserProfileSettings,
  type UserProfileOverview,
} from '../api'

type ProfileTab = 'info' | 'materials' | 'abilities'
type ProfileDrawer = 'info' | 'material' | 'ability' | null
type AssessmentMode = 'overview' | 'questions' | 'result'

const materialStatus: Record<ProfileMaterial['status'], string> = {
  draft: '等待确认',
  processing: '正在分析',
  ready: '已解析并使用',
  failed: '分析失败',
}

const employmentLabels: Record<JobTarget['employment_type'], string> = {
  internship: '实习',
  campus: '校招',
  social: '社招',
}

function formatDate(value?: string) {
  if (!value) return '尚未更新'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '尚未更新'
  return new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric' }).format(date)
}

function errorMessage(error: unknown) {
  if (error instanceof APIError) return error.message
  return error instanceof Error ? error.message : '操作失败，请稍后重试'
}

function capabilitySource(capability: ProfileCapability) {
  if (capability.status === 'verified') return '能力验证'
  if (capability.status === 'evidence_backed') return '简历与经历证据'
  return '材料不足'
}

export function UserProfilePage({ target }: { target: JobTarget | null }) {
  const [activeTab, setActiveTab] = useState<ProfileTab>('abilities')
  const [drawer, setDrawer] = useState<ProfileDrawer>(null)
  const [profile, setProfile] = useState<UserProfileOverview | null>(null)
  const [materials, setMaterials] = useState<ProfileMaterial[]>([])
  const [settings, setSettings] = useState<UserProfileSettings>({ existing_experience: '' })
  const [weeklyHours, setWeeklyHours] = useState('')
  const [expectedWeeks, setExpectedWeeks] = useState('')
  const [existingExperience, setExistingExperience] = useState('')
  const [selectedAbilityId, setSelectedAbilityId] = useState<string | null>(null)
  const [editingMaterial, setEditingMaterial] = useState<ProfileMaterial | null>(null)
  const [materialType, setMaterialType] = useState<ProfileMaterialType>('resume')
  const [materialTitle, setMaterialTitle] = useState('')
  const [materialText, setMaterialText] = useState('')
  const [selectedFileName, setSelectedFileName] = useState('')
  const [assessmentMode, setAssessmentMode] = useState<AssessmentMode>('overview')
  const [practiceMode, setPracticeMode] = useState<ProfilePracticeMode>('validation')
  const [session, setSession] = useState<ProfilePracticeSession | null>(null)
  const [answers, setAnswers] = useState<Record<string, string>>({})
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')

  const refresh = async () => {
    const [profileResponse, materialsResponse, settingsResponse] = await Promise.all([
      jobPilotAPI.getUserProfile(),
      jobPilotAPI.listProfileMaterials(),
      jobPilotAPI.getProfileSettings(),
    ])
    setProfile(profileResponse.profile)
    setMaterials(materialsResponse.materials ?? [])
    setSettings(settingsResponse.settings)
  }

  useEffect(() => {
    let active = true
    setLoading(true)
    refresh()
      .catch((loadError) => { if (active) setError(errorMessage(loadError)) })
      .finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [])

  const abilities = profile?.capabilities ?? []
  const assessedCount = profile?.assessed_count ?? 0
  const pendingCount = profile?.pending_count ?? 0
  const selectedAbility = useMemo(
    () => abilities.find((ability) => ability.concept_id === selectedAbilityId) ?? null,
    [abilities, selectedAbilityId],
  )
  const requiredInfoCount = Number(Boolean(target)) * 2
    + Number(Boolean(settings.weekly_hours))
    + Number(Boolean(settings.expected_weeks))

  const closeDrawer = () => {
    setDrawer(null)
    setSelectedAbilityId(null)
    setEditingMaterial(null)
    setAssessmentMode('overview')
    setSession(null)
    setAnswers({})
    setError('')
  }

  useEffect(() => {
    if (!drawer) return
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy) closeDrawer()
    }
    window.addEventListener('keydown', closeOnEscape)
    return () => window.removeEventListener('keydown', closeOnEscape)
  }, [drawer, busy])

  const openAbility = (capability: ProfileCapability) => {
    setSelectedAbilityId(capability.concept_id)
    setAssessmentMode('overview')
    setSession(null)
    setAnswers({})
    setError('')
    setDrawer('ability')
  }

  const openMaterial = (material?: ProfileMaterial) => {
    setEditingMaterial(material ?? null)
    setMaterialType(material?.type ?? 'resume')
    setMaterialTitle(material?.title ?? '')
    setMaterialText(material?.text ?? '')
    setSelectedFileName('')
    setError('')
    setDrawer('material')
  }

  const extractMaterialFile = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return
    setBusy(true)
    setError('')
    setSelectedFileName(file.name)
    try {
      const extracted = await jobPilotAPI.extractProfileMaterial(file)
      if (!materialTitle.trim()) setMaterialTitle(extracted.title)
      setMaterialText(extracted.text)
    } catch (extractError) {
      setSelectedFileName('')
      setError(errorMessage(extractError))
    } finally {
      setBusy(false)
    }
  }

  const openInfo = () => {
    setWeeklyHours(settings.weekly_hours?.toString() ?? '')
    setExpectedWeeks(settings.expected_weeks?.toString() ?? '')
    setExistingExperience(settings.existing_experience)
    setError('')
    setDrawer('info')
  }

  const saveSettings = async () => {
    setBusy(true)
    setError('')
    try {
      const response = await jobPilotAPI.saveProfileSettings({
        weekly_hours: Number(weeklyHours),
        expected_weeks: Number(expectedWeeks),
        existing_experience: existingExperience.trim(),
      })
      setSettings(response.settings)
      setNotice('必要信息已保存。')
      closeDrawer()
    } catch (saveError) {
      setError(errorMessage(saveError))
    } finally {
      setBusy(false)
    }
  }

  const saveMaterial = async () => {
    setBusy(true)
    setError('')
    try {
      const input = { type: materialType, title: materialTitle.trim(), text: materialText.trim() }
      if (editingMaterial) await jobPilotAPI.updateProfileMaterial(editingMaterial.id, input)
      else await jobPilotAPI.createProfileMaterial(input)
      await refresh()
      setNotice(editingMaterial ? '材料已更新，请确认内容后重新分析。' : '材料草稿已保存，请确认后再交给模型分析。')
      closeDrawer()
    } catch (saveError) {
      setError(errorMessage(saveError))
    } finally {
      setBusy(false)
    }
  }

  const confirmMaterial = async (material: ProfileMaterial) => {
    if (!window.confirm(`确认分析“${material.title}”吗？系统只接受能够回链到原文的能力证据。`)) return
    setBusy(true)
    setError('')
    try {
      await jobPilotAPI.confirmProfileMaterial(material.id)
      await refresh()
      setNotice('材料分析完成，用户画像已按真实证据更新。')
    } catch (confirmError) {
      setError(errorMessage(confirmError))
    } finally {
      setBusy(false)
    }
  }

  const deleteMaterial = async (material: ProfileMaterial) => {
    if (!window.confirm(`删除“${material.title}”吗？材料和对应证据会一并删除。`)) return
    setBusy(true)
    setError('')
    try {
      await jobPilotAPI.deleteProfileMaterial(material.id)
      await refresh()
      setNotice('材料已删除。已有能力等级不会因此下降。')
    } catch (deleteError) {
      setError(errorMessage(deleteError))
    } finally {
      setBusy(false)
    }
  }

  const startPractice = async (mode: ProfilePracticeMode) => {
    if (!selectedAbility) return
    setBusy(true)
    setError('')
    setPracticeMode(mode)
    try {
      const response = await jobPilotAPI.startProfileSession(selectedAbility.concept_id, mode)
      setSession(response.session)
      setAnswers({})
      setAssessmentMode('questions')
    } catch (practiceError) {
      setError(errorMessage(practiceError))
    } finally {
      setBusy(false)
    }
  }

  const submitPractice = async () => {
    if (!session) return
    setBusy(true)
    setError('')
    try {
      const response = await jobPilotAPI.submitProfileSession(
        session.id,
        session.questions.map((question) => ({ question_id: question.id, answer: answers[question.id]?.trim() ?? '' })),
      )
      setSession(response.session)
      setAssessmentMode('result')
      await refresh()
    } catch (submitError) {
      setError(errorMessage(submitError))
    } finally {
      setBusy(false)
    }
  }

  const primaryAction = () => {
    if (activeTab === 'info') openInfo()
    if (activeTab === 'materials') openMaterial()
    if (activeTab === 'abilities') {
      const pending = abilities.find((ability) => ability.current_level === 0) ?? abilities[0]
      if (pending) openAbility(pending)
    }
  }

  const primaryLabel = activeTab === 'info' ? '编辑信息' : activeTab === 'materials' ? '添加材料' : '补齐能力'
  const progress = abilities.length > 0 ? Math.round(assessedCount / abilities.length * 100) : 0

  return (
    <div className="page-content profile-page">
      <div className="page-heading-row">
        <div><h1>用户画像</h1><p className="page-description">先用已有材料判断能力，只对证据不足的部分继续补充。</p></div>
        <button className="button button-primary profile-primary-button" type="button" onClick={primaryAction} disabled={loading || (activeTab === 'abilities' && abilities.length === 0)}>
          {activeTab === 'info' ? <Pencil size={15} /> : activeTab === 'materials' ? <Plus size={16} /> : <MessageSquareText size={16} />}{primaryLabel}
        </button>
      </div>

      {notice && <div className="market-notice" role="status"><CheckCircle2 size={17} /><span>{notice}</span><button type="button" onClick={() => setNotice('')} aria-label="关闭提示"><X size={15} /></button></div>}
      {error && !drawer && <div className="notice" role="alert"><span>{error}</span><button type="button" onClick={() => setError('')} aria-label="关闭提示">×</button></div>}

      <section className="profile-progress" aria-label="用户画像完成进度">
        <div className="progress-copy"><span className="eyebrow">画像完成度</span><strong>{loading ? '正在读取真实画像……' : `${assessedCount} / ${abilities.length} 项能力已评估`}</strong><p>{profile?.market_profile_ready ? `还剩 ${pendingCount} 项岗位能力需要补充证据或验证。` : abilities.length > 0 ? '市场画像尚未达到 JD 门槛，当前能力清单会随新 JD 继续更新。' : '请先补充市场画像，系统才能确定需要评估的岗位能力。'}</p></div>
        <div className="progress-meter" aria-hidden="true"><span style={{ width: `${progress}%` }} /></div>
        <div className="profile-counts"><div><strong>{requiredInfoCount} / 4</strong><span>必要信息</span></div><div><strong>{profile?.material_count ?? 0}</strong><span>已有材料</span></div><div><strong>{pendingCount}</strong><span>待评估</span></div></div>
      </section>

      <div className="market-tabs profile-tabs" role="tablist" aria-label="用户画像内容">
        <button className={activeTab === 'info' ? 'is-current' : ''} type="button" role="tab" aria-selected={activeTab === 'info'} onClick={() => setActiveTab('info')}>必要信息 <span>{requiredInfoCount}</span></button>
        <button className={activeTab === 'materials' ? 'is-current' : ''} type="button" role="tab" aria-selected={activeTab === 'materials'} onClick={() => setActiveTab('materials')}>简历与经历 <span>{materials.length}</span></button>
        <button className={activeTab === 'abilities' ? 'is-current' : ''} type="button" role="tab" aria-selected={activeTab === 'abilities'} onClick={() => setActiveTab('abilities')}>岗位能力 <span>{abilities.length}</span></button>
      </div>

      {activeTab === 'abilities' && <section className="market-panel profile-panel" aria-labelledby="profile-ability-title">
        <div className="market-panel-head"><div><h2 id="profile-ability-title">当前目标需要的能力</h2><p>只显示当前目标相关能力；其他已评估结果继续保留。</p></div><span className="pending-summary">{pendingCount} 项待评估</span></div>
        {loading ? <div className="market-empty"><LoaderCircle className="spin" size={22} /><p>正在读取用户画像……</p></div>
          : abilities.length === 0 ? <div className="market-empty"><MessageSquareText size={24} /><h3>还没有可评估的岗位能力</h3><p>{profile?.market_profile_ready ? '市场画像中暂时没有能力数据。' : '完成市场画像和能力等级标准后，这里会显示真实能力清单。'}</p></div>
            : <><div className="profile-ability-head" aria-hidden="true"><span>能力</span><span>用户当前等级</span><span>市场画像等级</span><span>判断来源</span><span>更新时间</span><span /></div><div className="profile-ability-list">{abilities.map((ability) => <button className={`profile-ability-row ${ability.current_level > 0 ? '' : 'is-pending'}`} type="button" key={ability.concept_id} onClick={() => openAbility(ability)}><strong>{ability.name}</strong><span>{ability.current_level > 0 ? <b className="level-chip">L{ability.current_level}</b> : <b className="pending-chip">待评估</b>}</span><span>{ability.market_level_ready ? <b className="target-level">L{ability.market_level}</b> : <b className="target-level">准备中</b>}</span><span className="ability-source">{capabilitySource(ability)}</span><span className="ability-updated">{formatDate(ability.updated_at)}</span><ChevronRight size={16} /></button>)}</div></>}
      </section>}

      {activeTab === 'info' && <section className="profile-info-grid" aria-label="必要信息">
        <article><span>目标岗位</span><strong>{target?.title ?? '尚未设置'}</strong><p>{target ? (target.directions.length > 1 ? '复合目标' : '单一目标') : '请从顶部设置求职目标'}</p></article>
        <article><span>求职类型</span><strong>{target ? `${target.graduation_year ? `${target.graduation_year} ` : ''}${employmentLabels[target.employment_type]}` : '尚未设置'}</strong><p>当前求职阶段</p></article>
        <article><span>每周投入</span><strong>{settings.weekly_hours ? `${settings.weekly_hours} 小时` : '尚未设置'}</strong><p>用于项目开发与准备</p></article>
        <article><span>期望周期</span><strong>{settings.expected_weeks ? `${settings.expected_weeks} 周` : '尚未设置'}</strong><p>完成一项可写入简历的项目</p></article>
        <article className="experience-card"><span>已有经历</span><strong>{settings.existing_experience || (materials.length > 0 ? materials.map((item) => item.title).join('、') : '尚未补充')}</strong><p>系统会优先从简历和经历材料中提取详细依据。</p></article>
      </section>}

      {activeTab === 'materials' && <section className="market-panel materials-panel" aria-labelledby="materials-title">
        <div className="market-panel-head"><div><h2 id="materials-title">简历与经历材料</h2><p>系统先使用这些材料覆盖岗位能力，材料不足时才会提问。</p></div></div>
        {materials.length === 0 ? <div className="market-empty"><FileText size={24} /><h3>还没有材料</h3><p>添加简历文本或经历自述，保存后由你确认是否交给模型分析。</p></div>
          : <div className="material-list">{materials.map((material) => <article className="material-row" key={material.id}><span className="material-file-icon"><FileText size={17} /></span><div><strong>{material.title}</strong><span>{material.type === 'resume' ? '简历' : '经历自述'} · {formatDate(material.updated_at)}</span><p>{material.status === 'failed' ? material.failure_reason : material.status === 'ready' ? `已提取 ${material.evidence_count} 条能力证据` : material.text.slice(0, 80)}</p></div><span className="material-status">{materialStatus[material.status]}</span><div className="material-actions">{material.status !== 'processing' && <button className="icon-button" type="button" title="编辑材料" onClick={() => openMaterial(material)}><Pencil size={15} /></button>}{(material.status === 'draft' || material.status === 'failed') && <button className="icon-button" type="button" title="确认并分析" disabled={busy} onClick={() => confirmMaterial(material)}><RotateCw size={15} /></button>}{material.status !== 'processing' && <button className="icon-button" type="button" title="删除材料" disabled={busy} onClick={() => deleteMaterial(material)}><Trash2 size={15} /></button>}</div></article>)}</div>}
      </section>}

      {drawer && <div className="drawer-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !busy) closeDrawer() }}><aside className="market-drawer profile-drawer" role="dialog" aria-modal="true" aria-labelledby="profile-drawer-title">
        <header className="drawer-header"><div><span>{drawer === 'ability' ? '岗位能力评估' : drawer === 'material' ? '补充判断依据' : '必要信息'}</span><h2 id="profile-drawer-title">{drawer === 'ability' ? selectedAbility?.name : drawer === 'material' ? (editingMaterial ? '编辑材料' : '添加简历或经历') : '当前必要信息'}</h2></div><button className="icon-button" type="button" onClick={closeDrawer} disabled={busy} aria-label="关闭抽屉"><X size={19} /></button></header>

        {drawer === 'ability' && selectedAbility && <div className="drawer-body ability-assessment">
          <div className="ability-comparison"><div><span>用户当前等级</span><strong>{selectedAbility.current_level > 0 ? `L${selectedAbility.current_level}` : '待评估'}</strong></div><div><span>市场画像等级</span><strong>{selectedAbility.market_level_ready ? `L${selectedAbility.market_level}` : '准备中'}</strong></div></div>
          <section className="evidence-card"><span>现有判断依据</span>{selectedAbility.evidence.length > 0 ? selectedAbility.evidence.map((item) => <div className="profile-evidence-item" key={item.id}><p>“{item.quote}”</p><small>{item.reason} · 支持 L{item.level}</small></div>) : <p>现有材料还没有提供能够回链到原文的能力证据。</p>}<small>来源：{capabilitySource(selectedAbility)}</small></section>
          {error && <p className="form-error" role="alert">{error}</p>}
          {assessmentMode === 'overview' && <section className="assessment-choice"><h3>{selectedAbility.current_level > 0 ? '继续验证或复习' : '选择补充方式'}</h3>{selectedAbility.current_level === 0 && <p>没有证据时不会直接记为 L0；可以先补材料，或通过多道技术与场景题验证。</p>}{selectedAbility.needs_validation && <button type="button" disabled={busy} onClick={() => startPractice('validation')}><MessageSquareText size={18} /><span><strong>验证下一级能力</strong><small>一次只验证一级，未通过不会降级</small></span><ChevronRight size={17} /></button>}<button type="button" disabled={busy} onClick={() => startPractice('review')}><RotateCw size={18} /><span><strong>生成复习题</strong><small>获得反馈，但不会修改能力等级</small></span><ChevronRight size={17} /></button>{busy && <p><LoaderCircle className="spin" size={16} /> 正在生成问题……</p>}</section>}
          {assessmentMode === 'questions' && session && <section className="question-card profile-question-list"><span>{practiceMode === 'validation' ? `本次验证 L${session.base_level} → L${session.target_level}` : `本次复习 L${session.target_level}`}</span>{session.questions.map((question, index) => <label key={question.id}><strong>{index + 1}. {question.prompt}</strong><small>{question.dimension}</small><textarea value={answers[question.id] ?? ''} onChange={(event) => setAnswers((current) => ({ ...current, [question.id]: event.target.value }))} placeholder="结合原理、真实场景和你的处理过程回答……" /></label>)}<div className="drawer-actions"><button className="button" type="button" disabled={busy} onClick={() => setAssessmentMode('overview')}>返回</button><button className="button button-primary" type="button" disabled={busy || session.questions.some((question) => !(answers[question.id] ?? '').trim())} onClick={submitPractice}>{busy ? '评估中……' : '提交全部回答'}</button></div></section>}
          {assessmentMode === 'result' && session?.evaluation && <section className="question-card profile-practice-result"><span>{session.level_updated ? '验证通过' : practiceMode === 'review' ? '复习完成' : '本次未通过'}</span><h3>{session.level_updated ? `能力已更新为 L${session.target_level}` : practiceMode === 'review' ? '本次结果不会修改等级' : '原能力等级保持不变'}</h3><p>{session.evaluation.summary}</p><strong>综合得分：{Math.round(session.evaluation.score * 100)}%</strong>{session.evaluation.question_results.map((result, index) => <div className="profile-question-result" key={result.question_id}><b>{result.passed ? '通过' : '需要补充'} · 第 {index + 1} 题</b><p>{result.feedback}</p></div>)}<div className="drawer-actions"><button className="button button-primary" type="button" onClick={closeDrawer}>完成</button></div></section>}
        </div>}

        {drawer === 'material' && <div className="drawer-body material-form"><label className={`upload-card ${busy ? 'is-busy' : ''}`}><input type="file" accept=".pdf,.docx,.txt,.md,.markdown,application/pdf,application/vnd.openxmlformats-officedocument.wordprocessingml.document,text/plain,text/markdown" disabled={busy} onChange={extractMaterialFile} /><Upload size={21} /><strong>{busy ? '正在提取文件文字……' : selectedFileName ? `已读取：${selectedFileName}` : '点击选择 PDF、DOCX、TXT 或 Markdown'}</strong><span>系统只在内存中提取文字，不保存原文件；提取后请检查内容。</span></label><label>材料类型<select value={materialType} onChange={(event) => setMaterialType(event.target.value as ProfileMaterialType)}><option value="resume">简历文本</option><option value="experience">经历自述</option></select></label><label>材料标题<input value={materialTitle} maxLength={120} onChange={(event) => setMaterialTitle(event.target.value)} placeholder="例如：后端开发实习简历" /></label><label htmlFor="material-text">完整内容</label><textarea id="material-text" value={materialText} onChange={(event) => setMaterialText(event.target.value)} placeholder="选择文件自动提取文字，或直接粘贴简历、项目经历和科研经历……" />{error && <p className="form-error" role="alert">{error}</p>}<div className="drawer-actions"><button className="button" type="button" disabled={busy} onClick={closeDrawer}>取消</button><button className="button button-primary" type="button" disabled={busy || materialTitle.trim().length === 0 || materialText.trim().length < 20} onClick={saveMaterial}>{busy ? '处理中……' : <>保存草稿 <ArrowRight size={15} /></>}</button></div></div>}

        {drawer === 'info' && <div className="drawer-body profile-info-form"><p>目标岗位和求职类型从页面顶部修改；这里补充项目准备需要的时间与已有经历。</p><label>目标岗位<input value={target?.title ?? '尚未设置'} readOnly /></label><label>求职类型<input value={target ? `${target.graduation_year ? `${target.graduation_year} ` : ''}${employmentLabels[target.employment_type]}` : '尚未设置'} readOnly /></label><label>每周可投入时间<span className="profile-number-input"><input type="number" min="1" max="80" value={weeklyHours} onChange={(event) => setWeeklyHours(event.target.value)} placeholder="例如：12" /><b>小时/周</b></span></label><label>期望项目周期<span className="profile-number-input"><input type="number" min="1" max="52" value={expectedWeeks} onChange={(event) => setExpectedWeeks(event.target.value)} placeholder="例如：10" /><b>周</b></span></label><label>已有经历<textarea value={existingExperience} onChange={(event) => setExistingExperience(event.target.value)} placeholder="概括项目、科研和技术实践经历……" /></label>{error && <p className="form-error" role="alert">{error}</p>}<div className="drawer-actions"><button className="button" type="button" disabled={busy} onClick={closeDrawer}>取消</button><button className="button button-primary" type="button" disabled={busy || Number(weeklyHours) < 1 || Number(expectedWeeks) < 1} onClick={saveSettings}>{busy ? '保存中……' : '保存信息'}</button></div></div>}
      </aside></div>}
    </div>
  )
}
