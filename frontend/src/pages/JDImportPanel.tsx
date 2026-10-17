import { useState } from 'react'
import type { JDImportSession } from '../api'

const errors: Record<string, string> = { model_auth_failed: '模型 Key 无效，请检查设置后重试。', model_rate_limited: '模型调用受到限流，可稍后重试。', model_output_truncated: '模型输出被截断，可重试识别。', too_many_jd_candidates: '识别出的岗位超过 50 份，请拆分材料后录入。', model_configuration_unavailable: '模型配置不可用，请检查设置后重试。' }

export function JDImportPanel({ session, onConfirm, onRetry }: { session: JDImportSession; onConfirm: (ids: string[]) => Promise<void>; onRetry: () => Promise<void> }) {
  const [selected, setSelected] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const run = async (action: () => Promise<void>) => { if (busy) return; setBusy(true); setError(''); try { await action() } catch (e) { setError(e instanceof Error ? e.message : '操作失败') } finally { setBusy(false) } }
  if (session.status !== 'ready' && session.status !== 'failed') return null
  return <section className="market-panel jd-import-panel" aria-label="岗位识别结果">
    <header className="market-panel-head"><div><h2>{session.status === 'failed' ? '岗位识别暂时失败' : `识别到 ${session.candidates.length} 个岗位，请选择`}</h2>
      <p>{session.status === 'failed' ? errors[session.error_code ?? ''] ?? '本次识别未完成，材料已保存，可以重试。' : session.replacement_jd_id ? '选择一份用于更新原 JD，原记录编号会保留。' : '确认后只导入所选岗位，原始材料会保留。'}</p>
    </div></header>
    {session.status === 'ready' && <div className="jd-import-candidates">{session.candidates.map(candidate => <label className="jd-import-candidate" key={candidate.id}>
      <input type={session.replacement_jd_id ? 'radio' : 'checkbox'} name={`candidate-${session.id}`} checked={selected.includes(candidate.id)} disabled={busy || !!candidate.jd_id} onChange={event => setSelected(current => session.replacement_jd_id ? [candidate.id] : event.target.checked ? [...current, candidate.id] : current.filter(id => id !== candidate.id))} />
      <div><strong>{candidate.title || '未标注岗位名称'}</strong><p>{candidate.company || '未标注公司'}</p><details><summary>查看识别出的原文</summary><div className="raw-jd-text">{candidate.raw_text}</div></details></div>
    </label>)}<button type="button" className="button button-primary" disabled={busy || !selected.length} onClick={() => void run(() => onConfirm(selected))}>{busy ? '正在导入…' : `确认导入 ${selected.length} 份`}</button></div>}
    {session.status === 'failed' && <div className="jd-import-candidates"><button className="button" disabled={busy} onClick={() => void run(onRetry)}>重新识别</button></div>}
    {error && <p className="market-error" role="alert">{error}</p>}
  </section>
}
