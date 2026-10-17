import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  AlertCircle,
  ArrowRight,
  CheckCircle2,
  ChevronRight,
  Clock3,
  FileText,
  Pencil,
  Plus,
  RotateCcw,
  Search,
  SearchCheck,
  Trash2,
  Upload,
  X,
} from 'lucide-react'
import { jobPilotAPI, type JobDescription, type MarketAbilitySummary } from '../api'
import { useMarketData } from './useMarketData'
import { JDImportPanel } from './JDImportPanel'
import { JDImportNotice, useJDImportFeedback } from './JDImportNotice'

type MarketTab = 'jobs' | 'abilities'
type DrawerMode = 'add' | 'edit' | 'detail' | 'ability' | null
type JDStatus = 'included' | 'processing' | 'reference' | 'excluded' | 'failed'

interface JDSample {
	analysis?: JobDescription['analysis']
  id: string
  title: string
  company: string
  status: JDStatus
  statusLabel: string
  primaryCategory: string
  secondaryCategory: string
  updatedLabel: string
  reason: string
  abilities: string[]
  abilityEvidence?: Array<{ name: string; evidence: string }>
  abilityLevels?: JobDescription['ability_levels']
  responsibilities?: string[]
  analysisModel?: string
  conditions: string
  rawText: string
  jobStatus?: JobDescription['job_status']
  validationStatus?: JobDescription['validation_status']
	abilityReview?: JobDescription['ability_review']
	abilityGrading?: JobDescription['ability_grading']
}

const statusIcons: Record<JDStatus, typeof CheckCircle2> = {
  included: CheckCircle2,
  processing: Clock3,
  reference: SearchCheck,
  excluded: X,
  failed: AlertCircle,
}

const statusLabels: Record<JDStatus, string> = {
  included: '计入画像',
  processing: '正在处理',
  reference: '仅作参考',
  excluded: '未计入',
  failed: '处理失败',
}

function formatUpdatedLabel(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit',
  }).format(date)
}

function commonLevelLabel(ability: MarketAbilitySummary) {
	const usesPreferred = ability.level_summary.sample_count === 0 && ability.preferred_level_summary.sample_count > 0
	const summary = usesPreferred ? ability.preferred_level_summary : ability.level_summary
	if (summary.status === 'processing' || summary.status === 'pending') return '等级判定中'
	if (summary.status === 'failed' && summary.sample_count === 0) return '等级判定失败'
	if (summary.common_levels.length === 0) return '暂无等级'
	const prefix = usesPreferred ? '加分项' : ''
	if (summary.common_levels.length === 1) return `${prefix}常见 L${summary.common_levels[0]}`
	return `${prefix}${summary.common_levels.map((level) => `L${level}`).join('、')} 均较常见`
}

function displayedLevelSummary(ability: MarketAbilitySummary) {
	return ability.level_summary.sample_count === 0 && ability.preferred_level_summary.sample_count > 0
		? ability.preferred_level_summary
		: ability.level_summary
}

function levelSourceLabel(source: 'explicit' | 'inferred') {
	return source === 'explicit' ? 'JD 明确要求' : '系统根据职责推断'
}

function requirementKindLabel(kind: 'required' | 'preferred' | 'unspecified') {
	if (kind === 'preferred') return '加分要求'
	if (kind === 'required') return '必需要求'
	return '普通要求'
}

function analysisFailureCopy(code?: string) {
	switch (code) {
		case 'classification_unresolved': return { label: '岗位分类未完成', reason: '已提取的内容已保留，岗位职责暂时无法对应合法分类，可补充原文后重试分类。' }
		case 'classification_rejected': return { label: '岗位分类审核未通过', reason: '岗位未能匹配已有分类，新增分类审核也未通过；已提取的内容仍然保留。' }
		case 'classification_review_not_configured': return { label: '岗位分类审核未配置', reason: '已提取内容已保存，请配置平台分类审核服务后重试此阶段。' }
		case 'model_invalid_response':
			return {
				label: '内容结构需要重试',
				reason: '本阶段返回的结构仍无法处理。已保存的内容会保留，重试会从失败阶段继续。',
			}
		case 'model_not_configured':
		case 'model_configuration_unavailable':
			return {
				label: '模型配置不可用',
				reason: '当前模型配置无法用于 JD 分析，请检查设置后重新分析。',
			}
		case 'catalog_unavailable':
			return {
				label: '目录暂时不可用',
				reason: '岗位或能力目录暂时不可用，JD 原文已保存，可稍后重新分析。',
			}
		case 'ability_normalization_unavailable':
			return {
				label: '能力归一化未配置',
				reason: '已提取内容已保存，请稍后重试能力处理。',
			}
		case 'ability_vectors_unavailable':
			return {
				label: '能力目录准备中',
				reason: '能力目录向量尚未准备好，JD 原文已保存，可稍后重新分析。',
			}
		default:
			return {
				label: '分析失败',
				reason: '本次分析暂时失败，JD 原文已安全保存，可稍后重新分析。',
			}
	}
}

function mapJD(item: JobDescription): JDSample {
	const pollable = item.job_status === 'queued' || item.job_status === 'running'
	const extracted = item.job_status === 'succeeded'
	const invalid = item.validation_status === 'invalid'
	const incomplete = item.validation_status === 'incomplete'
	const technicalFailure = item.job_status === 'failed'
	const failureCopy = analysisFailureCopy(item.job_error_code)
	const displayStatus: JDStatus = technicalFailure
	  ? 'failed'
	  : invalid || incomplete
	    ? 'excluded'
	    : pollable
	      ? 'processing'
	      : item.status
	const statusLabel = invalid
	  ? '无法识别为岗位 JD'
	  : incomplete
	    ? 'JD 信息不完整'
	    : technicalFailure
	      ? failureCopy.label
	      : extracted && item.status === 'processing'
	        ? '基础解析完成，等待分类'
	        : statusLabels[displayStatus]
	const waitingForClassification = extracted && item.validation_status === 'valid' && item.status === 'processing'
	const missingSecondaryCategory = pollable
	  ? '等待分类'
	  : technicalFailure
	    ? '未完成'
	    : extracted
	      ? '无'
	      : '等待分类'
  return {
    analysis: item.analysis,
    id: item.id,
    title: item.title || (pollable ? '正在识别岗位名称' : '岗位名称未提取'),
    company: item.company || (pollable ? '待识别公司' : '公司名称未提取'),
		status: displayStatus,
		statusLabel,
		primaryCategory: invalid || incomplete ? '不进入分类' : item.primary_category || '等待分类',
		secondaryCategory: invalid || incomplete ? '不进入能力归一化' : item.secondary_category || missingSecondaryCategory,
    updatedLabel: formatUpdatedLabel(item.updated_at),
		reason: item.validation_reason || (technicalFailure ? failureCopy.reason : item.reason) || (pollable
		  ? 'JD 原文已经保存，持久化分析任务正在等待处理。'
		  : waitingForClassification
		    ? '基础信息已经解析，等待岗位分类和能力归一化。'
		    : '等待系统补充判断依据。'),
    abilities: item.ability_mentions?.map((mention) => mention.name) ?? [],
    abilityEvidence: item.ability_mentions ?? [],
		abilityLevels: item.ability_levels ?? [],
    responsibilities: item.responsibilities ?? [],
    analysisModel: item.analysis_model,
		conditions: item.conditions || (pollable ? '等待分析' : '尚未提取'),
		rawText: item.raw_text,
		jobStatus: item.job_status,
		validationStatus: item.validation_status,
		abilityReview: item.ability_review ?? { pending_count: 0, failed_count: 0 },
		abilityGrading: item.ability_grading ?? { status: 'not_started' },
  }
}

export function MarketPage() {
  const [activeTab, setActiveTab] = useState<MarketTab>('jobs')
  const [drawerMode, setDrawerMode] = useState<DrawerMode>(null)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [selectedAbilityId, setSelectedAbilityId] = useState<string | null>(null)
  const [rawJD, setRawJD] = useState('')
	const [entryMode, setEntryMode] = useState<'single' | 'batch'>('single')
	const [editRawJD, setEditRawJD] = useState('')
	const [searchQuery, setSearchQuery] = useState('')
	const [statusFilter, setStatusFilter] = useState<'all' | JDStatus>('all')
  const data = useMarketData(searchQuery, statusFilter)
  const { profile, imports, loading, error, setError, reload: loadJDs } = data
  const samples = useMemo(() => data.items.map(mapJD), [data.items])
  const allSamples = useMemo(() => data.all.map(mapJD), [data.all])
  const [notice, setNotice] = useState('')
  const clearNotice = useCallback(() => setNotice(''), [])
  const importFeedback = useJDImportFeedback(imports, clearNotice)
  const [submitting, setSubmitting] = useState(false)
  const selectedJD = useMemo(
    () => samples.find((sample) => sample.id === selectedId) ?? null,
    [samples, selectedId],
  )
  const selectedAbility = useMemo<MarketAbilitySummary | null>(
    () => profile.abilities.find((ability) => ability.ability_id === selectedAbilityId) ?? null,
    [profile.abilities, selectedAbilityId],
  )
	const includedCount = profile.included_jd_count
  const processingCount = allSamples.filter((sample) => sample.status === 'processing').length
	const notIncludedCount = allSamples.length - includedCount - processingCount

  const closeDrawer = () => {
    setDrawerMode(null)
    setSelectedId(null)
    setSelectedAbilityId(null)
  }

  useEffect(() => {
    if (!drawerMode) return
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !(event.target instanceof Element && event.target.closest('dialog[open]'))) closeDrawer()
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [drawerMode])

  const addJD = async () => {
    const content = rawJD.trim()
    if (!content || submitting) return
    setSubmitting(true)
    setError('')
    try {
      const session = await jobPilotAPI.startJDImport(content)
      importFeedback.submitted(session)
      setRawJD('')
      closeDrawer()
	  await loadJDs()
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : '保存 JD 失败')
    } finally {
      setSubmitting(false)
    }
  }

	const updateJD = async () => {
		if (!selectedJD || !editRawJD.trim() || submitting) return
		setSubmitting(true)
		setError('')
		try {
			const session = await jobPilotAPI.startJDImport(editRawJD, selectedJD.id)
			closeDrawer()
			importFeedback.submitted(session)
			await loadJDs(false)
		} catch (updateError) {
			setError(updateError instanceof Error ? updateError.message : '更新 JD 失败')
		} finally {
			setSubmitting(false)
		}
	}

	const deleteJD = async () => {
		if (!selectedJD || !window.confirm(`确定删除“${selectedJD.title}”吗？删除后将立即从市场画像中移除。`)) return
		setError('')
		try {
			await jobPilotAPI.deleteJD(selectedJD.id)
			data.remove(selectedJD.id)
			closeDrawer()
			setNotice('JD 已删除，市场画像已同步更新。')
			await loadJDs()
		} catch (deleteError) {
			setError(deleteError instanceof Error ? deleteError.message : '删除 JD 失败')
		}
	}

	const importTextFile = async (file?: File) => {
		if (!file) return
		if (!file.name.toLowerCase().endsWith('.txt')) {
			setError('批量导入目前支持 UTF-8 编码的 .txt 文件。')
			return
		}
		setEntryMode('batch')
		setRawJD(await file.text())
	}

	const applyUpdatedJD = (updated: JobDescription) => data.upsert(updated)

  const retryJD = async (id: string) => {
    setError('')
    try {
      const current = allSamples.find(sample => sample.id === id)
      if (current && !current.analysis?.stages.length) {
        const session = await jobPilotAPI.startJDImport(current.rawText, id)
        importFeedback.submitted(session)
        await loadJDs(false)
        return
      }
      const updated = await jobPilotAPI.retryJD(id)
      applyUpdatedJD(updated)
      setNotice('分析任务已重新进入队列。')
    } catch (retryError) {
      setError(retryError instanceof Error ? retryError.message : '重新分析失败')
    }
  }

	const retryAbilityReviews = async (id: string) => {
		setError('')
		try {
			const updated = await jobPilotAPI.retryAbilityReviews(id)
			applyUpdatedJD(updated)
			setNotice('能力目录审核已重新进入队列。')
		} catch (retryError) {
			setError(retryError instanceof Error ? retryError.message : '重新审核失败')
		}
	}

	const retryAbilityGrading = async (id: string) => {
		setError('')
		try {
			const updated = await jobPilotAPI.retryAbilityGrading(id)
			applyUpdatedJD(updated)
			setNotice('能力等级判定已重新进入队列。')
		} catch (retryError) {
			setError(retryError instanceof Error ? retryError.message : '重新判级失败')
		}
	}

  return (
    <div className="page-content market-page">
      <div className="page-heading-row">
        <div>
          <h1>市场画像</h1>
          <p className="page-description">用目标岗位的真实 JD，整理市场要求和岗位能力清单。</p>
        </div>
        <div className="market-heading-actions">
          {importFeedback.pendingCount > 0 && <button className="button" type="button" onClick={importFeedback.openProgress}>查看识别进度（{importFeedback.pendingCount}）</button>}
          <button className="button button-primary add-jd-button" type="button" onClick={() => setDrawerMode('add')}>
            <Plus size={16} aria-hidden="true" />
            添加 JD
          </button>
        </div>
      </div>

      {notice && (
        <div className="market-notice" role="status">
          <CheckCircle2 size={17} aria-hidden="true" />
          <span>{notice}</span>
          <button type="button" onClick={() => setNotice('')} aria-label="关闭提示"><X size={15} /></button>
        </div>
      )}
      {error && <div className="market-error" role="alert"><AlertCircle size={17} /><span>{error}</span></div>}

      {importFeedback.visible && <JDImportNotice emptyCount={importFeedback.emptyCount} queuedCount={importFeedback.queuedCount} runningCount={importFeedback.runningCount} onDismiss={importFeedback.dismiss} onAdd={() => setDrawerMode('add')} />}

      {imports.map(session => <JDImportPanel key={session.id} session={session}
        onConfirm={async ids => { await jobPilotAPI.confirmJDImport(session.id, ids); setNotice('所选岗位已导入，正在分别提取内容并处理分类与能力。'); await loadJDs(false) }}
        onRetry={async () => { const retried = await jobPilotAPI.retryJDImport(session.id); importFeedback.submitted(retried); await loadJDs(false) }} />)}

      <section className="market-progress" aria-label="市场画像完成进度">
        <div className="progress-copy">
          <span className="eyebrow">完成门槛</span>
		  <strong>{includedCount} / {profile.required_jd_count} 份相关 JD</strong>
		  <p>{profile.complete ? '已达到市场画像门槛，可以继续添加更多 JD。' : `还差 ${Math.max(profile.required_jd_count - includedCount, 0)} 份处理完成且符合当前目标的 JD。`}</p>
        </div>
		<div className="progress-meter" aria-hidden="true"><span style={{ width: `${Math.min(includedCount / profile.required_jd_count * 100, 100)}%` }} /></div>
        <div className="market-counts">
          <div><strong>{includedCount}</strong><span>已计入</span></div>
          <div><strong>{processingCount}</strong><span>处理中</span></div>
          <div><strong>{notIncludedCount}</strong><span>未计入</span></div>
        </div>
      </section>

      <div className="market-tabs" role="tablist" aria-label="市场画像内容">
        <button className={activeTab === 'jobs' ? 'is-current' : ''} type="button" role="tab" aria-selected={activeTab === 'jobs'} onClick={() => setActiveTab('jobs')}>
          JD 样本 <span>{samples.length}</span>
        </button>
        <button className={activeTab === 'abilities' ? 'is-current' : ''} type="button" role="tab" aria-selected={activeTab === 'abilities'} onClick={() => setActiveTab('abilities')}>
		  岗位能力 <span>{profile.abilities.length}</span>
        </button>
      </div>

      {activeTab === 'jobs' ? (
        <section className="market-panel" aria-labelledby="jd-list-title">
          <div className="market-panel-head">
            <div>
              <h2 id="jd-list-title">JD 样本</h2>
              <p>处理完成后，系统才会判断是否计入当前目标。</p>
            </div>
			<div className="jd-list-tools">
				<label className="jd-search-box">
					<Search size={16} aria-hidden="true" />
					<input value={searchQuery} onChange={(event) => setSearchQuery(event.target.value)} placeholder="搜索公司、能力或分类" aria-label="搜索 JD" />
					{searchQuery && <button type="button" onClick={() => setSearchQuery('')} aria-label="清空搜索"><X size={14} /></button>}
				</label>
				<select value={statusFilter} onChange={(event) => setStatusFilter(event.target.value as 'all' | JDStatus)} aria-label="按状态筛选">
					<option value="all">全部状态</option>
					<option value="included">计入画像</option>
					<option value="reference">仅作参考</option>
					<option value="excluded">已排除</option>
					<option value="processing">处理中</option>
					<option value="failed">处理失败</option>
				</select>
			</div>
          </div>
          <div className="jd-table-head" aria-hidden="true">
            <span>岗位</span><span>分类</span><span>与当前目标关系</span><span>更新时间</span><span />
          </div>
          <div className="jd-list">
            {loading && <div className="market-empty">正在读取当前目标下的 JD……</div>}
			{!loading && samples.length === 0 && <div className="market-empty">{searchQuery || statusFilter !== 'all' ? '没有符合当前筛选条件的 JD。' : '还没有 JD。可粘贴招聘网页文字开始建立市场画像。'}</div>}
            {samples.map((sample) => {
              const StatusIcon = statusIcons[sample.status]
              return (
                <button
                  className="jd-row"
                  type="button"
                  key={sample.id}
                  onClick={() => {
                    setSelectedId(sample.id)
                    setDrawerMode('detail')
                  }}
                >
                  <span className="jd-title-cell">
                    <span className={`status-icon status-${sample.status}`}><StatusIcon size={16} /></span>
                    <span><strong>{sample.title}</strong><small>{sample.company}</small></span>
                  </span>
                  <span className="jd-category-cell"><strong>{sample.primaryCategory}</strong><small>{sample.secondaryCategory}</small></span>
                  <span><span className={`status-badge status-${sample.status}`}>{sample.statusLabel}</span></span>
                  <span className="jd-updated">{sample.updatedLabel}</span>
                  <ChevronRight className="jd-chevron" size={17} aria-hidden="true" />
                </button>
              )
            })}
          </div>
        </section>
      ) : (
        <section className="market-panel ability-panel" aria-labelledby="ability-list-title">
          <div className="market-panel-head">
            <div>
              <h2 id="ability-list-title">岗位能力清单</h2>
              <p>能力清单将在 JD 解析和能力归一化链路完成后，由已计入的 JD 自动汇总。</p>
            </div>
          </div>
		  {profile.abilities.length === 0 ? <div className="market-empty">还没有已计入 JD 的能力结果。</div> : <div className="ability-list">
			{profile.abilities.map((ability) => <div className="ability-row" key={ability.ability_id}>
			  <div className="ability-name"><strong>{ability.name}</strong></div>
			  <div><span className="ability-label">{ability.category}</span><p>可以满足当前样本中 {ability.covered_jd_count} 份 JD 的相关要求</p></div>
			  <div className={`ability-level-summary is-${displayedLevelSummary(ability).status}`}><strong>{commonLevelLabel(ability)}</strong><span>{displayedLevelSummary(ability).sample_count > 0 ? `基于 ${displayedLevelSummary(ability).sample_count} 份 JD` : '等待独立判级'}</span></div>
			  <button className="ability-evidence" type="button" onClick={() => {
				setSelectedAbilityId(ability.ability_id)
				setDrawerMode('ability')
			  }}><strong>{ability.covered_jd_count} / {profile.included_jd_count} 份 JD</strong><span>查看 {ability.evidences.length} 处原文依据</span></button>
			</div>)}
		  </div>}
        </section>
      )}

      {drawerMode && (
        <div className="drawer-backdrop" role="presentation" onMouseDown={(event) => {
          if (event.target === event.currentTarget) closeDrawer()
        }}>
          <aside className="market-drawer" role="dialog" aria-modal="true" aria-labelledby="drawer-title">
            <header className="drawer-header">
              <div>
				<span>{drawerMode === 'add' ? '录入岗位' : drawerMode === 'edit' ? '修改原文' : drawerMode === 'ability' ? selectedAbility?.category : selectedJD?.company}</span>
				<h2 id="drawer-title">{drawerMode === 'add' ? '添加 JD' : drawerMode === 'edit' ? selectedJD?.title : drawerMode === 'ability' ? selectedAbility?.name : selectedJD?.title}</h2>
              </div>
              <button className="icon-button" type="button" onClick={closeDrawer} aria-label="关闭抽屉"><X size={19} /></button>
            </header>

            {drawerMode === 'add' ? (
              <div className="drawer-body add-jd-form">
				<div className="drawer-intro"><FileText size={19} /><p>直接粘贴招聘网页文字，允许包含导航、广告和多个岗位。识别出多个岗位后，由你选择要导入的岗位。</p></div>
				<div className="entry-mode-switch" role="tablist" aria-label="导入方式">
					<button type="button" className={entryMode === 'single' ? 'is-current' : ''} onClick={() => setEntryMode('single')}>单份录入</button>
					<button type="button" className={entryMode === 'batch' ? 'is-current' : ''} onClick={() => setEntryMode('batch')}>批量导入</button>
				</div>
				<label htmlFor="raw-jd">{entryMode === 'single' ? '招聘材料' : '包含多个岗位的材料'}</label>
				<textarea id="raw-jd" value={rawJD} onChange={(event) => setRawJD(event.target.value)} placeholder={entryMode === 'single' ? '粘贴招聘网页上的岗位描述，混有无关文字也可以……' : '粘贴多个岗位的描述，系统会识别候选，再由你选择。'} autoFocus />
				{entryMode === 'batch' && <label className="text-file-import"><Upload size={16} /><span>从 .txt 文件读取</span><input type="file" accept=".txt,text/plain" onChange={(event) => void importTextFile(event.target.files?.[0])} /></label>}
				<p className="field-help">可直接粘贴网页文字，系统会忽略导航和广告。识别到多个岗位后先选择，再确认导入；每次最多 50 份。</p>
                <div className="drawer-actions">
                  <button className="button" type="button" onClick={closeDrawer}>取消</button>
                  <button className="button button-primary" type="button" onClick={addJD} disabled={!rawJD.trim() || submitting}>
					{submitting ? '保存中……' : entryMode === 'batch' ? '保存并识别岗位' : '保存并识别岗位'} <ArrowRight size={15} />
                  </button>
                </div>
              </div>
			) : drawerMode === 'edit' && selectedJD ? (
				<div className="drawer-body add-jd-form">
					<div className="drawer-intro"><Pencil size={19} /><p>保存后先识别岗位；确认导入后更新原记录并重新分析，原记录编号会保留。</p></div>
					<label htmlFor="edit-raw-jd">招聘材料</label>
					<textarea id="edit-raw-jd" value={editRawJD} onChange={(event) => setEditRawJD(event.target.value)} autoFocus />
					<p className="field-help">允许混有无关文字。多个岗位需要选择一份更新原记录；与已有 JD 重复时会显示对应记录。</p>
					<div className="drawer-actions">
						<button className="button" type="button" onClick={() => setDrawerMode('detail')}>取消</button>
						<button className="button button-primary" type="button" onClick={() => void updateJD()} disabled={!editRawJD.trim() || editRawJD.trim() === selectedJD.rawText.trim() || submitting}>{submitting ? '保存中……' : '保存并重新分析'} <ArrowRight size={15} /></button>
					</div>
				</div>
            ) : drawerMode === 'ability' && selectedAbility ? (
              <div className="drawer-body jd-detail">
                <section className="detail-status-card ability-coverage-card">
                  <SearchCheck size={18} aria-hidden="true" />
                  <div><strong>可满足 {selectedAbility.covered_jd_count} / {profile.included_jd_count} 份 JD</strong><p>这里只表示该能力对岗位要求的覆盖范围，与用户是否掌握无关。</p></div>
                </section>
				<section>
				  <h3>市场常见要求等级</h3>
				  <div className="market-level-overview">
					<div><span>常见等级</span><strong>{commonLevelLabel(selectedAbility)}</strong></div>
					<div><span>判级样本</span><strong>{displayedLevelSummary(selectedAbility).sample_count} 份 JD</strong></div>
					<div><span>判断来源</span><strong>{displayedLevelSummary(selectedAbility).explicit_count} 份明确 · {displayedLevelSummary(selectedAbility).inferred_count} 份推断</strong></div>
					<div><span>少数更高要求</span><strong>{displayedLevelSummary(selectedAbility).higher_requirement_count} 份</strong></div>
				  </div>
				  {displayedLevelSummary(selectedAbility).distribution.length > 0
					? <div className="level-distribution">{displayedLevelSummary(selectedAbility).distribution.map((item) => <span key={item.level}><strong>L{item.level}</strong>{item.count} 份</span>)}</div>
					: <p className="detail-muted">{selectedAbility.level_summary.status === 'failed' ? '独立等级判定暂时失败，能力覆盖统计仍然有效。' : '独立等级判定正在进行，完成后会自动显示。'}</p>}
				</section>
				{displayedLevelSummary(selectedAbility).evidences.length > 0 && <section>
				  <h3>等级判断依据</h3>
				  <div className="level-evidence-list">{displayedLevelSummary(selectedAbility).evidences.map((item) => <article key={`${item.job_description_id}-${item.requirement_kind}`}>
					<header><strong>{item.job_title}</strong><span>L{item.level} · {levelSourceLabel(item.source)}</span></header>
					<p>{item.evidence}</p><small>{item.reason}</small>
				  </article>)}</div>
				</section>}
				{selectedAbility.level_summary.sample_count > 0 && selectedAbility.preferred_level_summary.sample_count > 0 && <section>
				  <h3>加分要求</h3>
				  <p className="detail-muted">加分项单独统计，不会抬高必备能力的常见等级。</p>
				  <div className="level-distribution">{selectedAbility.preferred_level_summary.distribution.map((item) => <span key={item.level}><strong>L{item.level}</strong>{item.count} 份</span>)}</div>
				</section>}
                <section>
                  <h3>JD 原文依据</h3>
                  <div className="ability-evidence-list">
                    {selectedAbility.evidences.map((item, index) => <p key={`${item.job_description_id}-${item.evidence}-${index}`}>
                      <strong>{item.job_title}{item.qualifier ? ` · ${item.qualifier}` : ''}</strong>
                      <span>{item.evidence}</span>
                    </p>)}
                  </div>
                </section>
              </div>
            ) : selectedJD ? (
              <div className="drawer-body jd-detail">
                <section className="detail-status-card">
                  {(() => {
                    const DetailStatusIcon = statusIcons[selectedJD.status]
                    return <span className={`status-icon status-${selectedJD.status}`}><DetailStatusIcon size={17} /></span>
                  })()}
                  <div><strong>{selectedJD.statusLabel}</strong><p>{selectedJD.reason}</p></div>
                </section>
                <section>
                  <h3>岗位分类</h3>
                  <div className="classification-grid">
                    <div><span>主导分类</span><strong>{selectedJD.primaryCategory}</strong></div>
                    <div><span>次要关联</span><strong>{selectedJD.secondaryCategory}</strong></div>
                  </div>
                </section>
                <section>
                  <h3>主要职责</h3>
                  {selectedJD.responsibilities?.length
                    ? <ul className="jd-fact-list">{selectedJD.responsibilities.map((item) => <li key={item}>{item}</li>)}</ul>
                    : <p className="detail-muted">处理完成后显示。</p>}
                </section>
                <section>
                  <h3>提取的能力</h3>
                  {selectedJD.abilities.length > 0 ? <div className="ability-tags">{selectedJD.abilities.map((ability) => <span key={ability}>{ability}</span>)}</div> : <p className="detail-muted">处理完成后显示。</p>}
				  {selectedJD.abilityLevels?.length ? <div className="jd-ability-level-list">{selectedJD.abilityLevels.map((item) => <article key={`${item.ability_id}-${item.requirement_kind}`}>
					<header><strong>{item.name}</strong><span>L{item.level} · {levelSourceLabel(item.source)} · {requirementKindLabel(item.requirement_kind)}</span></header>
					<p>{item.evidence}</p><small>{item.reason}</small>
				  </article>)}</div> : null}
				  {selectedJD.abilityGrading?.stale && !!selectedJD.abilityLevels?.length && <p className="detail-muted">以上为上次成功的判级结果，资料或等级标准已变化，暂不参与当前市场等级统计。</p>}
				  {selectedJD.abilityGrading?.paused
					? <p className="detail-muted">这份 JD 暂未计入当前目标，已保留判级结果和任务；再次计入后复用有效结果或继续判级。</p>
					: selectedJD.abilityGrading?.status === 'failed'
					  ? <div className="detail-status-card"><AlertCircle size={17} /><div><strong>能力等级判定暂时失败</strong><p>JD 解析结果和上次成功的等级仍然保留，可以单独重新判级。</p><button className="button retry-button" type="button" onClick={() => void retryAbilityGrading(selectedJD.id)}><RotateCcw size={15} />重新判级</button></div></div>
					  : selectedJD.abilityGrading?.status === 'queued' || selectedJD.abilityGrading?.status === 'running'
						? <p className="detail-muted">正在按每项能力的 L0～L5 标准判断这份 JD 的要求等级。</p>
						: !selectedJD.abilityLevels?.length && selectedJD.abilities.length > 0 ? <p className="detail-muted">能力等级尚未生成。</p> : null}
				  {!selectedJD.abilityLevels?.length && selectedJD.abilityEvidence?.length ? <div className="ability-evidence-list">{selectedJD.abilityEvidence.map((item) => <p key={`${item.name}-${item.evidence}`}><strong>{item.name}</strong><span>{item.evidence}</span></p>)}</div> : null}
                </section>
				{(selectedJD.abilityReview?.pending_count ?? 0) > 0 && <section className="detail-status-card">
					<Clock3 size={17} /><div><strong>{selectedJD.abilityReview?.pending_count} 项能力待目录审核</strong><p>{selectedJD.abilityReview?.blocked ? '平台审核配置完成并重启服务后会自动继续。已确认能力仍正常计入画像。' : selectedJD.abilityReview?.next_attempt_at && new Date(selectedJD.abilityReview.next_attempt_at).getTime() > Date.now() + 60_000 ? `预计 ${formatUpdatedLabel(selectedJD.abilityReview.next_attempt_at)} 恢复处理。已确认能力仍正常计入画像。` : '未确认候选不会展示或计入统计；已确认能力仍正常计入画像。'}</p></div>
				</section>}
				{(selectedJD.abilityReview?.failed_count ?? 0) > 0 && <section className="detail-status-card">
					<AlertCircle size={17} /><div><strong>能力目录审核暂时失败</strong><p>JD 分析结果仍然保留，可以单独重新审核。</p><button className="button retry-button" type="button" onClick={() => void retryAbilityReviews(selectedJD.id)}><RotateCcw size={15} />重新审核</button></div>
				</section>}
                <section>
                  <h3>学历与其他条件</h3>
                  <p>{selectedJD.conditions}</p>
                </section>
                <section>
                  <h3>JD 原文</h3>
                  <div className="raw-jd-text">{selectedJD.rawText}</div>
                </section>
				{!!selectedJD.analysis?.stages.length && <section><h3>处理进度</h3><ul className="jd-fact-list">{selectedJD.analysis.stages.map(stage => <li key={stage.name}>{({ extraction: '内容提取', classification: '岗位分类', ability_resolution: '能力归一化', classification_review: '岗位目录审核' } as Record<string, string>)[stage.name] ?? stage.name}：{stage.status === 'succeeded' ? '已完成' : stage.status === 'failed' ? analysisFailureCopy(stage.error_code).label : '处理中'}</li>)}</ul>{selectedJD.analysis.warnings?.map(warning => <p className="detail-muted" key={warning}>{warning}</p>)}</section>}
					{!!selectedJD.analysis?.requirements?.length && <section><h3>原始技术要求</h3><p className="detail-muted">保留原文要求关系；能力目录归属以审核结果为准。</p><ul className="jd-fact-list">{selectedJD.analysis.requirements.map((group,index)=><li key={index}>{group.RequirementKind==='preferred'?'加分：':''}{group.Operator==='any_of'?'任选一项：':group.Operator==='at_least_n'?`至少 ${group.RequiredCount} 项：`:''}{group.Options.map(option=>`${option.RawLabel}${option.Qualifier?`（${option.Qualifier}）`:''}`).join('、')}<details><summary>查看原文依据</summary><p>{group.Evidence}</p></details></li>)}</ul></section>}
					{!!selectedJD.analysis?.classifications?.length && <section><h3>分类依据与提议</h3><ul className="jd-fact-list">{selectedJD.analysis.classifications.map((item,index)=><li key={index}>{item.candidate?`待审核分类：${item.candidate.specialty_name}。`:''}{item.reason}<details><summary>查看职责依据</summary><p>{item.evidence}</p></details></li>)}</ul></section>}
				<div className="jd-management-actions">
					<button className="button" type="button" onClick={() => { setEditRawJD(selectedJD.rawText); setDrawerMode('edit') }}><Pencil size={15} />编辑原文</button>
					<button className="button danger-button" type="button" onClick={() => void deleteJD()}><Trash2 size={15} />删除 JD</button>
				</div>
                {selectedJD.analysisModel && <p className="analysis-version">由 {selectedJD.analysisModel} · {selectedJD.statusLabel}</p>}
				{selectedJD.jobStatus === 'failed'
                  ? <button className="button retry-button" type="button" onClick={() => void retryJD(selectedJD.id)}><RotateCcw size={15} />重新分析</button>
				  : (selectedJD.jobStatus === 'queued' || selectedJD.jobStatus === 'running') && <button className="button retry-button" type="button" onClick={() => void loadJDs()}><RotateCcw size={15} />重新检查状态</button>}
              </div>
            ) : null}
          </aside>
        </div>
      )}
    </div>
  )
}
