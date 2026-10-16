import { ArrowRight, Check, LockKeyhole } from 'lucide-react'
import type { LearningRouteView } from '../learningRouteTypes'

export interface RouteNavigation {
  onOpenMarket: () => void
  onOpenProfile: () => void
  onOpenProjects: () => void
  onOpenTarget: () => void
}

export function routeInputsReady(view: LearningRouteView | null) {
  const p = view?.preparation
  return Boolean(view?.readiness.code === 'ready' && p?.market_complete && p.profile_complete && p.analysis_ready && p.project_selected && !p.project_needs_review)
}

export function LearningRoutePreparation({ view, onOpenMarket, onOpenProfile, onOpenProjects, onOpenTarget }: RouteNavigation & { view: LearningRouteView }) {
  const p = view.preparation
  const ready = routeInputsReady(view)
  const targetMissing = view.readiness.code === 'target_missing'
  const steps = [
    { title: '市场画像', complete: p.market_complete, detail: `已计入 ${p.included_jd_count} / ${p.required_jd_count} 份目标 JD`, action: onOpenMarket, label: '补充 JD', enabled: !targetMissing },
    { title: '用户画像', complete: p.profile_complete, detail: p.profile_complete ? '必要信息与岗位能力评估已完成' : '补齐经历、投入时间和岗位能力评估', action: onOpenProfile, label: '完善画像', enabled: p.market_complete },
    { title: '审核与判级', complete: p.analysis_ready, detail: p.analysis_ready ? '相关能力审核与 JD 判级已完成' : '等待相关审核和判级完成；失败项可在市场画像重试', action: onOpenMarket, label: '查看分析进度', enabled: p.market_complete },
    { title: '推荐并选择项目', complete: p.project_selected && !p.project_needs_review, detail: p.project_selected ? `${view.project_title || '已选项目'}${p.project_needs_review ? ' · 依据变化，请重新核对' : ''}` : '从真实项目推荐中确认一个准备实践的项目', action: onOpenProjects, label: p.project_needs_review ? '核对项目' : '前往项目推荐', enabled: p.market_complete && p.profile_complete && p.analysis_ready },
  ]

  return <section className={`route-preparation ${ready ? 'is-ready' : ''}`} aria-labelledby="route-preparation-title">
    <div className="route-preparation-heading"><span className="route-preparation-icon" aria-hidden="true">{ready ? <Check size={20} /> : <LockKeyhole size={20} />}</span><div><h3 id="route-preparation-title">{ready ? '准备已完成，可以规划学习路线' : '先完成准备，再生成学习路线'}</h3><p>{ready ? '接下来分配两份计划的时间，再生成可编辑的预览。' : '学习路线依据两类画像和已选项目生成。完成以下条件后，开放预算设置和生成。'}</p></div></div>
    {!ready && <p className="route-preparation-reason" role="status">{view.readiness.message || '请先完成画像、审核判级和项目选择。'}</p>}
    {targetMissing && <button className="button button-primary" type="button" onClick={onOpenTarget}>设置求职目标 <ArrowRight size={14} /></button>}
    <ol className="route-preparation-steps">{steps.map((step, index) => <li className={step.complete ? 'is-complete' : ''} key={step.title}>
      <span className="route-step-number" aria-hidden="true">{step.complete ? <Check size={15} /> : index + 1}</span><div><strong>{step.title}</strong><p>{step.detail}</p></div><span className="route-step-state">{step.complete ? '已完成' : '待完成'}</span>
      {!step.complete && <button className="button" type="button" disabled={!step.enabled} onClick={step.action}>{step.label} <ArrowRight size={14} /></button>}
    </li>)}</ol>
    {!ready && <div className="route-plan-introduction"><p><strong>项目实践计划</strong>：围绕已选项目安排阶段、开发任务和验收成果。</p><p><strong>知识准备计划</strong>：根据当前差距补强知识，已达标时安排复习与验证。</p></div>}
  </section>
}
