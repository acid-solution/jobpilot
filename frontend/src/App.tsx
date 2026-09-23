import { useEffect, useState, type FormEvent } from 'react'
import {
  BookOpenCheck,
  BriefcaseBusiness,
  Check,
  Files,
  FolderGit2,
  LayoutDashboard,
  LoaderCircle,
  LogOut,
	Plus,
  ScanSearch,
  Settings,
	Trash2,
  UserRound,
  type LucideIcon,
} from 'lucide-react'
import { AgentWidget } from './components/AgentWidget'
import { AuthScreen } from './components/AuthScreen'
import {
  APIError,
  authAPI,
  jobPilotAPI,
  type AuthSession,
  type JobCategory,
  type JobTarget,
  type MarketProfile,
  type SaveTargetInput,
  type UserProfileOverview,
} from './api'
import { navItems } from './mockData'
import { KnowledgeGapsPage } from './pages/KnowledgeGapsPage'
import { MarketPage } from './pages/MarketPage'
import { ProjectsPage } from './pages/ProjectsPage'
import { SettingsPage } from './pages/SettingsPage'
import { UserProfilePage } from './pages/UserProfilePage'
import type { PageId } from './types'

const navIcons: Record<PageId, LucideIcon> = {
  dashboard: LayoutDashboard,
  market: BriefcaseBusiness,
  profile: UserRound,
  projects: FolderGit2,
  gaps: BookOpenCheck,
  settings: Settings,
}

function Dashboard({
  target,
  onOpenTarget,
  onOpenMarket,
  onOpenProfile,
  onOpenProjects,
}: {
  target: JobTarget | null
  onOpenTarget: () => void
  onOpenMarket: () => void
  onOpenProfile: () => void
  onOpenProjects: () => void
}) {
  const [market, setMarket] = useState<MarketProfile | null>(null)
  const [profile, setProfile] = useState<UserProfileOverview | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')

  useEffect(() => {
    let active = true
    setLoading(true)
    setLoadError('')
    Promise.all([jobPilotAPI.getMarketProfile(), jobPilotAPI.getUserProfile()])
      .then(([nextMarket, nextProfile]) => {
        if (!active) return
        setMarket(nextMarket)
        setProfile(nextProfile.profile)
      })
      .catch((error: unknown) => {
        if (active) setLoadError(error instanceof Error ? error.message : '读取画像进度失败')
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [target?.id])

  const targetComplete = Boolean(target)
  const marketComplete = Boolean(market?.complete)
  const totalCapabilities = profile?.capabilities.length ?? 0
  const profileComplete = Boolean(
    profile?.market_profile_ready && totalCapabilities > 0 && profile.pending_count === 0,
  )
  const remainingJDCount = Math.max(0, (market?.required_jd_count ?? 10) - (market?.included_jd_count ?? 0))
  const remainingStepCount = [targetComplete, marketComplete, profileComplete].filter((complete) => !complete).length
  const steps = [
    {
      id: 'target',
      title: '求职目标',
      description: target ? `${formatTarget(target)}，方向和求职类型已经确定` : '设置岗位方向和求职类型后开始建立画像',
      state: targetComplete ? '已完成' : '待设置',
      complete: targetComplete,
    },
    {
      id: 'market',
      title: '市场画像',
      description: loading
        ? '正在读取有效 JD 进度……'
        : `${market?.included_jd_count ?? 0} 份有效目标 JD 已计入，完成门槛为 ${market?.required_jd_count ?? 10} 份`,
      state: loading ? '读取中' : `${market?.included_jd_count ?? 0} / ${market?.required_jd_count ?? 10}`,
      complete: marketComplete,
    },
    {
      id: 'profile',
      title: '用户画像',
      description: loading
        ? '正在读取能力评估进度……'
        : totalCapabilities > 0
          ? `${profile?.assessed_count ?? 0} 项已评估，${profile?.pending_count ?? totalCapabilities} 项仍需材料或问答依据`
          : '市场画像完成后生成需要评估的岗位能力',
      state: loading ? '读取中' : `${profile?.assessed_count ?? 0} / ${totalCapabilities}`,
      complete: profileComplete,
    },
  ]

  const nextAction = !targetComplete
    ? { title: '下一步：设置求职目标', description: '先确定求职类型和岗位方向。', label: '设置目标', action: onOpenTarget }
    : !marketComplete
      ? {
          title: '下一步：补充目标 JD',
          description: remainingJDCount > 0 ? `再录入 ${remainingJDCount} 份符合当前目标的完整 JD。` : '继续检查正在分析的 JD。',
          label: '录入 JD',
          action: onOpenMarket,
        }
      : !profileComplete
        ? {
            title: '下一步：补全用户画像',
            description: `${profile?.pending_count ?? totalCapabilities} 项岗位能力还没有足够依据。`,
            label: '补齐能力',
            action: onOpenProfile,
          }
        : { title: '两类画像已经完成', description: '现在可以查看基于真实画像生成的项目推荐。', label: '查看项目', action: onOpenProjects }

  return (
    <div className="page-content">
      <h1>准备进度</h1>
      <p className="page-description">两类画像完成后，可以生成项目推荐和知识短板。</p>

      <section className="progress-panel" aria-labelledby="progress-title">
        <div className="panel-header">
          <h2 id="progress-title">画像准备</h2>
          <span>{loading ? '正在读取' : `${remainingStepCount} 项待完成`}</span>
        </div>
        <div className="steps">
          {steps.map((step) => (
            <div className="preparation-step" key={step.id}>
              <span className="step-icon" aria-hidden="true">
                {step.id === 'target' ? <Check size={17} /> : step.id === 'market' ? <Files size={17} /> : <ScanSearch size={17} />}
              </span>
              <div>
                <strong>{step.title}</strong>
                <p>{step.description}</p>
              </div>
              <span className={step.complete ? 'step-state is-complete' : 'step-state'}>{step.state}</span>
            </div>
          ))}
        </div>
      </section>

      {loadError && <p className="form-error dashboard-error" role="alert">{loadError}</p>}

      <section className="next-action" aria-labelledby="next-title">
        <div>
          <h2 id="next-title">{nextAction.title}</h2>
          <p>{nextAction.description}</p>
        </div>
        <button className="button button-primary" type="button" onClick={nextAction.action} disabled={loading}>{nextAction.label}</button>
      </section>

      <div className="result-grid">
        <section className="result-card">
          <strong>项目推荐</strong>
          <span>{marketComplete && profileComplete ? '画像已就绪，可以生成推荐' : '完成两类画像后解锁'}</span>
        </section>
        <section className="result-card">
          <strong>知识短板</strong>
          <span>{marketComplete && profileComplete ? '画像已就绪，可以比较能力差距' : '完成两类画像后解锁'}</span>
        </section>
      </div>
    </div>
  )
}

function buildNextStepPrompt(
  page: PageId,
  target: JobTarget | null,
  market: MarketProfile | null,
  profile: UserProfileOverview | null,
) {
  if (!target) return '你还没有设置求职目标。下一步先确定求职类型和岗位方向，系统才能开始建立市场画像。'

  const targetName = formatTarget(target)
  const included = market?.included_jd_count ?? 0
  const required = market?.required_jd_count ?? 10
  const remainingJDs = Math.max(0, required - included)
  const totalCapabilities = profile?.capabilities.length ?? 0
  const pendingCapabilities = profile?.pending_count ?? totalCapabilities

  if (!market?.complete) {
    const marketProgress = remainingJDs > 0
      ? `当前已计入 ${included}/${required} 份有效 JD，还需要补充 ${remainingJDs} 份。`
      : '当前仍有 JD 正在分析，请先检查处理结果。'
    if (page === 'market') return `${marketProgress}录入时优先选择职责与“${targetName}”直接相关的完整岗位说明。`
    return `根据当前真实进度，${marketProgress}市场画像达到门槛后，再继续补齐用户能力证据。`
  }

  if (pendingCapabilities > 0) {
    const profileProgress = `当前 ${totalCapabilities} 项目标岗位能力中，已有 ${profile?.assessed_count ?? 0} 项完成评估，还有 ${pendingCapabilities} 项缺少依据。`
    if (page === 'profile') return `${profileProgress}建议先提交已有简历和经历自述，材料覆盖不到的能力再通过问答验证。`
    return `${profileProgress}下一步进入用户画像补充材料或完成能力验证。`
  }

  if (page === 'projects') return '市场画像和用户画像已经完成。可以结合岗位需要、已有能力和项目体量查看项目推荐理由。'
  if (page === 'gaps') return '市场画像和用户画像已经完成。可以按市场常见要求与当前能力等级查看需要补齐的理论知识。'
  if (page === 'settings') return '当前画像准备已经完成。设置页可以管理模型连接和账号配置。'
  return '市场画像和用户画像已经完成。下一步可以查看项目推荐，或进入知识短板页检查理论能力差距。'
}

export default function App() {
  const [session, setSession] = useState<AuthSession | null>(null)
  const [authLoading, setAuthLoading] = useState(true)

  useEffect(() => {
    let active = true
    authAPI.setFailureHandler(() => {
      if (active) setSession(null)
    })
    authAPI.refresh()
      .then((refreshed) => {
        if (active) setSession(refreshed)
      })
      .catch(() => undefined)
      .finally(() => {
        if (active) setAuthLoading(false)
      })
    return () => {
      active = false
      authAPI.setFailureHandler(null)
    }
  }, [])

  if (authLoading) {
    return <div className="auth-loading"><span className="brand-mark">J</span><LoaderCircle className="spin" size={20} /><span>正在恢复登录状态……</span></div>
  }
  if (!session) {
    return <AuthScreen onAuthenticated={setSession} />
  }
  return <WorkspaceApp session={session} onSignedOut={() => setSession(null)} />
}

function WorkspaceApp({ session, onSignedOut }: { session: AuthSession; onSignedOut: () => void }) {
  const [currentPage, setCurrentPage] = useState<PageId>('dashboard')
  const [notice, setNotice] = useState('')
  const [target, setTarget] = useState<JobTarget | null>(null)
  const [targetDialogOpen, setTargetDialogOpen] = useState(false)
  const [targetLoading, setTargetLoading] = useState(true)
  const [signingOut, setSigningOut] = useState(false)
  const [nextStepPrompt, setNextStepPrompt] = useState('正在根据当前页面和画像进度生成下一步建议……')
  const currentLabel = navItems.find((item) => item.id === currentPage)?.label ?? '工作台'
  const targetLabel = target ? formatTarget(target) : targetLoading ? '正在读取……' : '尚未设置求职目标'
  const email = session.account.identities.find((identity) => identity.kind === 'email')?.value ?? '已登录用户'

  useEffect(() => {
    jobPilotAPI.getCurrentTarget()
	  .then((currentTarget) => {
		setTarget(currentTarget)
		if (currentTarget.catalog_status === 'reselection_required') {
		  setNotice('原有岗位方向无法完整匹配岗位目录，请重新选择。')
		  setTargetDialogOpen(true)
		}
	  })
      .catch((error: unknown) => {
        if (!(error instanceof APIError) || error.code !== 'target_required') {
          setNotice(error instanceof Error ? error.message : '读取求职目标失败')
        }
      })
      .finally(() => setTargetLoading(false))
  }, [])

  useEffect(() => {
    if (targetLoading) return
    if (!target) {
      setNextStepPrompt(buildNextStepPrompt(currentPage, null, null, null))
      return
    }

    let active = true
    Promise.allSettled([jobPilotAPI.getMarketProfile(), jobPilotAPI.getUserProfile()])
      .then(([marketResult, profileResult]) => {
        if (!active) return
        const market = marketResult.status === 'fulfilled' ? marketResult.value : null
        const profile = profileResult.status === 'fulfilled' ? profileResult.value.profile : null
        setNextStepPrompt(buildNextStepPrompt(currentPage, target, market, profile))
      })
    return () => {
      active = false
    }
  }, [currentPage, target, targetLoading])

  return (
    <div className="app-shell">
      <aside className="sidebar" aria-label="主导航">
        <div className="brand"><span className="brand-mark">J</span><span>JobPilot</span></div>
        <nav className="navigation">
          {navItems.map((item) => {
            const Icon = navIcons[item.id]
            return (
              <button
                className={item.id === currentPage ? 'nav-button is-current' : 'nav-button'}
                type="button"
                key={item.id}
                onClick={() => setCurrentPage(item.id)}
                aria-current={item.id === currentPage ? 'page' : undefined}
              >
                <Icon size={18} strokeWidth={1.8} aria-hidden="true" />
                {item.label}
              </button>
            )
          })}
        </nav>
        <div className="sidebar-note">Agent 修改资料前会先让你确认</div>
        <div className="sidebar-account">
          <span className="sidebar-avatar">{email.slice(0, 1).toUpperCase()}</span>
          <span className="sidebar-account-copy"><strong>{email}</strong><small>共享账号</small></span>
          <button
            type="button"
            aria-label="退出登录"
            disabled={signingOut}
            onClick={async () => {
              setSigningOut(true)
              try {
                await authAPI.logout()
              } finally {
                onSignedOut()
              }
            }}
          >
            <LogOut size={16} />
          </button>
        </div>
      </aside>

      <main className="main-area">
        <header className="topbar">
          <div>
            <span className="target-label">当前求职目标</span>
            <strong>{targetLabel}</strong>
          </div>
          <button
            className="button"
            type="button"
            onClick={() => setTargetDialogOpen(true)}
          >
            {target ? '编辑目标' : '设置目标'}
          </button>
        </header>
        {notice && (
          <div className="notice" role="status">
            <span>{notice}</span>
            <button type="button" onClick={() => setNotice('')} aria-label="关闭提示">×</button>
          </div>
        )}
        {currentPage === 'dashboard' ? (
          <Dashboard
            target={target}
            onOpenTarget={() => setTargetDialogOpen(true)}
            onOpenMarket={() => setCurrentPage('market')}
            onOpenProfile={() => setCurrentPage('profile')}
            onOpenProjects={() => setCurrentPage('projects')}
          />
        ) : currentPage === 'market' ? (
          <MarketPage key={target ? `${target.id}:${target.updated_at}` : 'no-target'} />
        ) : currentPage === 'profile' ? (
          <UserProfilePage target={target} />
        ) : currentPage === 'projects' ? (
          <ProjectsPage onOpenMarket={() => setCurrentPage('market')} onOpenProfile={() => setCurrentPage('profile')} />
        ) : currentPage === 'gaps' ? (
          <KnowledgeGapsPage onOpenMarket={() => setCurrentPage('market')} onOpenProfile={() => setCurrentPage('profile')} />
        ) : (
          <SettingsPage />
        )}
      </main>

      <AgentWidget
        currentPage={currentLabel}
        currentTarget={targetLabel}
        nextStepPrompt={nextStepPrompt}
      />

      {targetDialogOpen && (
        <TargetDialog
          target={target}
          onClose={() => setTargetDialogOpen(false)}
          onSaved={(saved) => {
            setTarget(saved)
            setTargetDialogOpen(false)
            setNotice('当前求职目标已保存。')
          }}
        />
      )}
    </div>
  )
}

const employmentLabels: Record<JobTarget['employment_type'], string> = {
  internship: '实习',
  campus: '校招',
  social: '社招',
}

function formatTarget(target: JobTarget) {
  const year = target.graduation_year ? `${target.graduation_year} ` : ''
  return `${target.title} · ${year}${employmentLabels[target.employment_type]}`
}

function TargetDialog({
  target,
  onClose,
  onSaved,
}: {
  target: JobTarget | null
  onClose: () => void
  onSaved: (target: JobTarget) => void
}) {
  const [employmentType, setEmploymentType] = useState<JobTarget['employment_type']>(target?.employment_type ?? 'internship')
  const [graduationYear, setGraduationYear] = useState(target?.graduation_year?.toString() ?? '2027')
	const [catalog, setCatalog] = useState<JobCategory[]>([])
	const [catalogLoading, setCatalogLoading] = useState(true)
	const [directions, setDirections] = useState<Array<{ category_id: string; specialty_id?: string }>>(
	  target?.directions.map((direction) => ({
		category_id: direction.category_id,
		specialty_id: direction.specialty_id,
	  })) ?? [],
	)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

	useEffect(() => {
	  jobPilotAPI.getJobCatalog()
		.then(setCatalog)
		.catch((loadError) => setError(loadError instanceof Error ? loadError.message : '读取岗位目录失败'))
		.finally(() => setCatalogLoading(false))
	}, [])

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const input: SaveTargetInput = {
      employment_type: employmentType,
      graduation_year: graduationYear ? Number(graduationYear) : undefined,
	  directions,
    }
    setSaving(true)
    setError('')
    try {
      onSaved(await jobPilotAPI.saveCurrentTarget(input))
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : '保存求职目标失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="drawer-backdrop" role="presentation" onMouseDown={(event) => {
      if (event.target === event.currentTarget) onClose()
    }}>
      <aside className="market-drawer" role="dialog" aria-modal="true" aria-labelledby="target-dialog-title">
        <header className="drawer-header">
          <div><span>JD 的统一归属边界</span><h2 id="target-dialog-title">设置当前求职目标</h2></div>
          <button className="icon-button" type="button" onClick={onClose} aria-label="关闭">×</button>
        </header>
        <form className="drawer-body target-form" onSubmit={submit}>
		  <div className="target-derived-title"><span>目标岗位</span><strong>{directions.length > 0 ? directions.map((direction) => {
			const category = catalog.find((item) => item.id === direction.category_id)
			return category?.specialties.find((item) => item.id === direction.specialty_id)?.name ?? category?.name
		  }).filter(Boolean).join('＋') : '请选择岗位方向'}</strong><small>名称会根据岗位目录中的选择自动生成</small></div>
          <label>求职类型
            <select value={employmentType} onChange={(event) => setEmploymentType(event.target.value as JobTarget['employment_type'])}>
              <option value="internship">实习</option><option value="campus">校招</option><option value="social">社招</option>
            </select>
          </label>
          <label>毕业年份<input type="number" min="2000" max="2100" value={graduationYear} onChange={(event) => setGraduationYear(event.target.value)} /></label>
		  <fieldset className="target-directions" disabled={catalogLoading}>
			<legend>岗位方向</legend>
			{directions.map((direction, index) => {
			  const category = catalog.find((item) => item.id === direction.category_id)
			  return <div className="target-direction-row" key={`${direction.category_id}-${index}`}>
				<select value={direction.category_id} aria-label={`第 ${index + 1} 个岗位大类`} onChange={(event) => setDirections((current) => current.map((item, itemIndex) => itemIndex === index ? { category_id: event.target.value } : item))}>
				  <option value="">选择岗位大类</option>
				  {catalog.map((item) => <option value={item.id} key={item.id}>{item.name}</option>)}
				</select>
				<select value={direction.specialty_id ?? ''} aria-label={`第 ${index + 1} 个细分方向`} disabled={!category} onChange={(event) => setDirections((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, specialty_id: event.target.value || undefined } : item))}>
				  <option value="">整个大类</option>
				  {category?.specialties.map((item) => <option value={item.id} key={item.id}>{item.name}</option>)}
				</select>
				<button className="icon-button" type="button" onClick={() => setDirections((current) => current.filter((_, itemIndex) => itemIndex !== index))} aria-label={`删除第 ${index + 1} 个岗位方向`}><Trash2 size={16} /></button>
			  </div>
			})}
			<button className="button target-add-direction" type="button" disabled={catalogLoading || directions.length >= 5 || catalog.length === 0} onClick={() => setDirections((current) => [...current, { category_id: catalog[0].id }])}><Plus size={15} />添加方向</button>
			<span>复合目标可以同时选择多个方向，最多 5 个。</span>
		  </fieldset>
          {error && <p className="form-error" role="alert">{error}</p>}
          <div className="drawer-actions">
            <button className="button" type="button" onClick={onClose}>取消</button>
			<button className="button button-primary" type="submit" disabled={saving || catalogLoading || directions.length === 0}>{saving ? '保存中……' : '保存目标'}</button>
          </div>
        </form>
      </aside>
    </div>
  )
}
