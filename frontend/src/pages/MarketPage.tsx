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
import { jobPilotAPI, type JobDescription, type MarketAbilitySummary, type MarketProfile } from '../api'

type MarketTab = 'jobs' | 'abilities'
type DrawerMode = 'add' | 'edit' | 'detail' | 'ability' | null
type JDStatus = 'included' | 'processing' | 'reference' | 'excluded' | 'failed'

interface JDSample {
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

interface AbilitySummary {
  name: string
  level: `L${0 | 1 | 2 | 3 | 4 | 5}`
  mentions: number
  expectation: string
  sourceLabel: string
}

const prototypeJDs: JDSample[] = [
  {
    id: 'jd-1',
    title: 'AI 应用后端实习生',
    company: 'A 公司',
    status: 'included',
    statusLabel: '计入画像',
    primaryCategory: '后端开发',
    secondaryCategory: 'AI 应用开发',
    updatedLabel: '今天 10:24',
    reason: '岗位职责同时包含 Go 后端服务、Agent 工作流和模型调用链路，符合当前复合目标。',
    abilities: ['Go', 'MySQL', 'Redis', 'Agent 开发', '大模型基础'],
    conditions: '本科及以上；计算机相关专业；每周实习 4 天及以上。',
    rawText: '负责 AI 应用平台的后端服务开发，参与 Agent 工作流、模型调用和知识库能力建设；要求熟悉 Go、MySQL、Redis，理解大模型应用开发。',
  },
  {
    id: 'jd-2',
    title: '智能体平台研发实习生',
    company: 'B 公司',
    status: 'included',
    statusLabel: '计入画像',
    primaryCategory: 'AI 应用开发',
    secondaryCategory: '后端开发',
    updatedLabel: '今天 09:51',
    reason: '主要工作是智能体平台开发，同时明确要求承担平台后端接口与任务调度。',
    abilities: ['Agent 开发', 'Python', 'RAG', '后端工程', '消息队列'],
    conditions: '本科及以上；有大模型应用或后端项目经验优先。',
    rawText: '参与智能体平台研发，负责工具调用、任务编排、知识检索及平台后端接口，要求掌握 Python 和常见后端组件。',
  },
  {
    id: 'jd-3',
    title: '智能客服后端研发实习生',
    company: 'C 公司',
    status: 'included',
    statusLabel: '计入画像',
    primaryCategory: '后端开发',
    secondaryCategory: 'AI 应用开发',
    updatedLabel: '昨天 18:02',
    reason: '实际职责包含对话 Agent 服务、上下文管理和后端工程，不只是把大模型列为加分项。',
    abilities: ['Go', 'MySQL', 'Redis', 'Agent 开发', 'Prompt 工程'],
    conditions: '硕士优先；可连续实习 3 个月。',
    rawText: '负责智能客服服务端与对话 Agent 能力建设，包括上下文管理、工具接入、效果分析和服务稳定性优化。',
  },
  {
    id: 'jd-4',
    title: '大模型应用研发实习生',
    company: 'D 公司',
    status: 'included',
    statusLabel: '计入画像',
    primaryCategory: 'AI 应用开发',
    secondaryCategory: '后端开发',
    updatedLabel: '昨天 16:40',
    reason: '岗位同时要求完成应用服务、RAG 链路和线上接口开发。',
    abilities: ['Python', 'RAG', '向量数据库', '后端工程', '大模型基础'],
    conditions: '本科及以上；熟悉至少一种后端语言。',
    rawText: '参与企业知识助手的 RAG 链路和应用服务研发，建设检索、重排、生成及评测能力，并负责相关后端接口。',
  },
  {
    id: 'jd-5',
    title: 'Agent 应用平台后端实习生',
    company: 'E 公司',
    status: 'included',
    statusLabel: '计入画像',
    primaryCategory: '后端开发',
    secondaryCategory: 'AI 应用开发',
    updatedLabel: '9 月 9 日',
    reason: '核心职责是 Agent 应用平台的服务端能力和工具执行基础设施。',
    abilities: ['Go', 'MySQL', '消息队列', 'Agent 开发', 'Docker'],
    conditions: '计算机相关专业；熟悉 Linux 开发环境。',
    rawText: '负责 Agent 应用平台后端开发，建设任务队列、工具执行、会话存储与可观测能力，要求熟悉 Go 和数据库。',
  },
  {
    id: 'jd-6',
    title: '知识库产品研发实习生',
    company: 'F 公司',
    status: 'included',
    statusLabel: '计入画像',
    primaryCategory: 'AI 应用开发',
    secondaryCategory: '后端开发',
    updatedLabel: '9 月 9 日',
    reason: '职责覆盖知识库问答效果和承载业务的后端服务，满足两个目标方向。',
    abilities: ['RAG', '向量数据库', 'Python', 'MySQL', '评测'],
    conditions: '本科及以上；有完整项目经验。',
    rawText: '负责知识库问答产品研发，完善文档处理、向量检索、回答生成和效果评测，并参与服务端工程建设。',
  },
  {
    id: 'jd-7',
    title: '智能研发平台实习生',
    company: 'G 公司',
    status: 'included',
    statusLabel: '计入画像',
    primaryCategory: '后端开发',
    secondaryCategory: 'AI 应用开发',
    updatedLabel: '9 月 8 日',
    reason: '岗位要求建设代码 Agent 的服务端、权限和任务执行链路。',
    abilities: ['Go', 'Agent 开发', '消息队列', '权限设计', '可观测性'],
    conditions: '硕士优先；熟悉 Git 和 Linux。',
    rawText: '参与代码 Agent 平台建设，负责服务端接口、任务编排、权限校验和执行过程观测，要求掌握 Go 或 Java。',
  },
  {
    id: 'jd-8',
    title: 'LLM 工程平台后端实习生',
    company: 'H 公司',
    status: 'included',
    statusLabel: '计入画像',
    primaryCategory: '后端开发',
    secondaryCategory: 'AI 应用开发',
    updatedLabel: '9 月 8 日',
    reason: '工作内容包含模型接入网关、Agent 调用链和后端平台工程。',
    abilities: ['Go', 'MySQL', 'Redis', '大模型基础', '可观测性'],
    conditions: '本科及以上；有高并发服务经验优先。',
    rawText: '建设 LLM 工程平台后端，负责模型网关、调用链路、限流与监控，并支持 Agent 应用接入。',
  },
  {
    id: 'jd-9',
    title: '业务后端开发实习生',
    company: 'I 公司',
    status: 'reference',
    statusLabel: '仅作参考',
    primaryCategory: '后端开发',
    secondaryCategory: '无明确关联',
    updatedLabel: '9 月 7 日',
    reason: '岗位以普通业务后端为主，只把大模型经验列为加分项，没有 Agent 开发职责。',
    abilities: ['Go', 'MySQL', 'Redis', '微服务'],
    conditions: '本科及以上；有服务端项目经验。',
    rawText: '负责业务系统后端研发，要求熟悉 Go、MySQL 和 Redis，有微服务经验优先；了解大模型者优先。',
  },
  {
    id: 'jd-10',
    title: 'Agent 应用研发实习生',
    company: 'J 公司',
    status: 'processing',
    statusLabel: '正在处理',
    primaryCategory: '等待分类',
    secondaryCategory: '等待分类',
    updatedLabel: '刚刚',
    reason: '原文已经保存，系统正在提取岗位分类和能力要求。',
    abilities: [],
    conditions: '等待分析',
    rawText: '负责 Agent 应用研发和后端接口建设，参与工具调用、知识库和线上服务优化。',
  },
]

const prototypeAbilitySummaries: AbilitySummary[] = [
  { name: 'Go', level: 'L3', mentions: 6, expectation: '能够独立完成服务端功能，并处理常见并发与稳定性问题', sourceLabel: '查看 6 处依据' },
  { name: 'Agent 开发', level: 'L3', mentions: 6, expectation: '理解工具调用、任务编排和上下文管理，并能独立完成应用落地', sourceLabel: '查看 6 处依据' },
  { name: 'MySQL', level: 'L3', mentions: 5, expectation: '能够独立完成业务建模、查询和常见数据一致性问题处理', sourceLabel: '查看 5 处依据' },
  { name: '大模型基础', level: 'L2', mentions: 4, expectation: '能够在指导下完成模型调用和基础效果对比，理解上下文限制', sourceLabel: '查看 4 处依据' },
  { name: 'Redis', level: 'L3', mentions: 4, expectation: '能够独立实现常见缓存或状态存储需求，处理失效与一致性问题', sourceLabel: '查看 4 处依据' },
  { name: 'RAG', level: 'L3', mentions: 3, expectation: '能够独立完成具体场景的检索、重排、生成和基础评测链路', sourceLabel: '查看 3 处依据' },
]

const statusIcons: Record<JDStatus, typeof CheckCircle2> = {
  included: CheckCircle2,
  processing: Clock3,
  reference: SearchCheck,
  excluded: X,
  failed: AlertCircle,
}

// 保留首轮视觉原型数据作为界面参考，真实页面不会读取这些记录。
void prototypeJDs
void prototypeAbilitySummaries

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

function splitBatchText(value: string) {
	return value
		.replace(/\r\n?/g, '\n')
		.split(/(?:^|\n)\s*-{3,}\s*(?:\n|$)|\f+/)
		.map((item) => item.trim())
		.filter(Boolean)
}

function analysisFailureCopy(code?: string) {
	switch (code) {
		case 'model_invalid_response':
			return {
				label: '模型结果未通过校验',
				reason: '模型返回的结构化结果不符合要求，系统自动重试后仍未通过。JD 原文已保存，可重新分析。',
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
    id: item.id,
    title: item.title || '正在识别岗位名称',
    company: item.company || '待识别公司',
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
  const [samples, setSamples] = useState<JDSample[]>([])
	const [allSamples, setAllSamples] = useState<JDSample[]>([])
	const [profile, setProfile] = useState<MarketProfile>({ included_jd_count: 0, required_jd_count: 10, complete: false, ability_grading_pending_count: 0, ability_grading_failed_count: 0, abilities: [] })
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)

  const loadJDs = useCallback(async (showLoading = true) => {
	if (showLoading) setLoading(true)
    setError('')
    try {
	  const filters = {
		statuses: statusFilter === 'all' ? undefined : [statusFilter],
		query: searchQuery,
	  }
	  const hasFilters = statusFilter !== 'all' || searchQuery.trim() !== ''
	  const filteredRequest = jobPilotAPI.listJDs(filters)
	  const [result, allResult, marketProfile] = await Promise.all([
		filteredRequest,
		hasFilters ? jobPilotAPI.listJDs() : filteredRequest,
		jobPilotAPI.getMarketProfile(),
	  ])
	  setSamples(result.map(mapJD))
	  setAllSamples(allResult.map(mapJD))
	  setProfile(marketProfile)
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : '读取 JD 失败')
    } finally {
	  if (showLoading) setLoading(false)
    }
  }, [searchQuery, statusFilter])

  useEffect(() => {
    void loadJDs()
  }, [loadJDs])

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
	const pollableCount = allSamples.filter((sample) => sample.jobStatus === 'queued' || sample.jobStatus === 'running' || ((sample.abilityReview?.pending_count ?? 0) > 0 && !sample.abilityReview?.blocked && (!sample.abilityReview?.next_attempt_at || new Date(sample.abilityReview.next_attempt_at).getTime() <= Date.now() + 60_000))).length + profile.ability_grading_pending_count
  const notIncludedCount = allSamples.length - includedCount - processingCount

  useEffect(() => {
	if (pollableCount === 0) return
    const timer = window.setInterval(() => {
	  void loadJDs(false)
    }, 2000)
    return () => window.clearInterval(timer)
	}, [loadJDs, pollableCount])

  const closeDrawer = () => {
    setDrawerMode(null)
    setSelectedId(null)
    setSelectedAbilityId(null)
  }

  useEffect(() => {
    if (!drawerMode) return
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeDrawer()
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
	  if (entryMode === 'single') {
		const created = await jobPilotAPI.createJD(content)
		setSamples((current) => [mapJD(created), ...current])
		setAllSamples((current) => [mapJD(created), ...current])
		setNotice('JD 原文和分析任务均已保存。分析完成前暂不计入市场画像。')
	  } else {
		const entries = splitBatchText(content)
		if (entries.length === 0 || entries.length > 50) {
			setError('一次需要导入 1～50 份 JD，请用单独一行 --- 分隔。')
			return
		}
		const result = await jobPilotAPI.createJDBatch(entries)
		setNotice(`批量处理完成：新增 ${result.created_count} 份，重复 ${result.duplicate_count} 份，无效 ${result.invalid_count} 份。`)
	  }
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
			const updated = await jobPilotAPI.updateJD(selectedJD.id, editRawJD)
			const mapped = mapJD(updated)
			setSamples((current) => current.map((sample) => sample.id === selectedJD.id ? mapped : sample))
			setAllSamples((current) => current.map((sample) => sample.id === selectedJD.id ? mapped : sample))
			setDrawerMode('detail')
			setNotice('JD 原文已更新，并已重新进入分析队列。')
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
			setSamples((current) => current.filter((sample) => sample.id !== selectedJD.id))
			setAllSamples((current) => current.filter((sample) => sample.id !== selectedJD.id))
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

  const retryJD = async (id: string) => {
    setError('')
    try {
      const updated = await jobPilotAPI.retryJD(id)
      setSamples((current) => current.map((sample) => sample.id === id ? mapJD(updated) : sample))
      setNotice('分析任务已重新进入队列。')
    } catch (retryError) {
      setError(retryError instanceof Error ? retryError.message : '重新分析失败')
    }
  }

	const retryAbilityReviews = async (id: string) => {
		setError('')
		try {
			const updated = await jobPilotAPI.retryAbilityReviews(id)
			setSamples((current) => current.map((sample) => sample.id === id ? mapJD(updated) : sample))
			setNotice('能力目录审核已重新进入队列。')
		} catch (retryError) {
			setError(retryError instanceof Error ? retryError.message : '重新审核失败')
		}
	}

  return (
    <div className="page-content market-page">
      <div className="page-heading-row">
        <div>
          <h1>市场画像</h1>
          <p className="page-description">用目标岗位的真实 JD，整理市场要求和岗位能力清单。</p>
        </div>
        <button className="button button-primary add-jd-button" type="button" onClick={() => setDrawerMode('add')}>
          <Plus size={16} aria-hidden="true" />
          添加 JD
        </button>
      </div>

      {notice && (
        <div className="market-notice" role="status">
          <CheckCircle2 size={17} aria-hidden="true" />
          <span>{notice}</span>
          <button type="button" onClick={() => setNotice('')} aria-label="关闭提示"><X size={15} /></button>
        </div>
      )}
      {error && <div className="market-error" role="alert"><AlertCircle size={17} /><span>{error}</span></div>}

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
			{!loading && samples.length === 0 && <div className="market-empty">{searchQuery || statusFilter !== 'all' ? '没有符合当前筛选条件的 JD。' : '还没有 JD。请粘贴一份完整岗位说明开始建立市场画像。'}</div>}
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
				<div className="drawer-intro"><FileText size={19} /><p>粘贴招聘页面中的完整岗位说明。系统会检查重复内容，再创建后台分析任务。</p></div>
				<div className="entry-mode-switch" role="tablist" aria-label="导入方式">
					<button type="button" className={entryMode === 'single' ? 'is-current' : ''} onClick={() => setEntryMode('single')}>单份录入</button>
					<button type="button" className={entryMode === 'batch' ? 'is-current' : ''} onClick={() => setEntryMode('batch')}>批量导入</button>
				</div>
				<label htmlFor="raw-jd">{entryMode === 'single' ? '完整 JD' : '多份完整 JD'}</label>
				<textarea id="raw-jd" value={rawJD} onChange={(event) => setRawJD(event.target.value)} placeholder={entryMode === 'single' ? '请粘贴岗位名称、岗位职责、任职要求等完整内容……' : '粘贴多份 JD，并在每两份之间单独输入一行 ---'} autoFocus />
				{entryMode === 'batch' && <label className="text-file-import"><Upload size={16} /><span>从 .txt 文件读取</span><input type="file" accept=".txt,text/plain" onChange={(event) => void importTextFile(event.target.files?.[0])} /></label>}
				<p className="field-help">{entryMode === 'single' ? '岗位名称、公司和分类由系统从原文识别。完全相同的 JD 不会重复保存。' : `使用单独一行 --- 分隔，每次最多 50 份。当前识别到 ${splitBatchText(rawJD).length} 份。`}</p>
                <div className="drawer-actions">
                  <button className="button" type="button" onClick={closeDrawer}>取消</button>
                  <button className="button button-primary" type="button" onClick={addJD} disabled={!rawJD.trim() || submitting}>
					{submitting ? '保存中……' : entryMode === 'batch' ? '批量保存并分析' : '保存并开始分析'} <ArrowRight size={15} />
                  </button>
                </div>
              </div>
			) : drawerMode === 'edit' && selectedJD ? (
				<div className="drawer-body add-jd-form">
					<div className="drawer-intro"><Pencil size={19} /><p>保存后会清除旧解析结果并重新分析，在新结果完成前这份 JD 不计入市场画像。</p></div>
					<label htmlFor="edit-raw-jd">完整 JD 原文</label>
					<textarea id="edit-raw-jd" value={editRawJD} onChange={(event) => setEditRawJD(event.target.value)} autoFocus />
					<p className="field-help">如果修改后的原文与当前目标下另一份 JD 完全相同，系统会拒绝保存。</p>
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
				  </article>)}</div>
				  : selectedJD.abilityGrading?.status === 'failed'
					? <p className="detail-muted">能力已经提取，但独立等级判定暂时失败；这不会影响 JD 的其他分析结果。</p>
					: selectedJD.abilities.length > 0 ? <p className="detail-muted">正在按每项能力的 L0～L5 标准判断这份 JD 的要求等级。</p> : null}
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
