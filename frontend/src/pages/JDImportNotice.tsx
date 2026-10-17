import { useCallback, useEffect, useRef, useState } from 'react'
import { AlertCircle, LoaderCircle, X } from 'lucide-react'
import type { JDImportSession } from '../api'

type FeedbackStage = 'empty' | 'processing'
const dismissalKey = (id: string, stage: FeedbackStage) => `jobpilot:jd-import:${stage}-dismissed:${id}`
const feedbackStage = (session: JDImportSession): FeedbackStage | null => session.status === 'empty' ? 'empty' : session.status === 'queued' || session.status === 'running' ? 'processing' : null

export function useJDImportFeedback(imports: JDImportSession[], clearNotice: () => void) {
  const announced = useRef(new Set<string>())
  const [emptyIds, setEmptyIds] = useState<string[]>([])
  const [progressOpen, setProgressOpen] = useState(false)
  const pending = imports.filter(session => feedbackStage(session) === 'processing')

  const announce = useCallback((sessions: JDImportSession[]) => {
    const fresh = sessions.filter(session => {
      const stage = feedbackStage(session)
      if (!stage || announced.current.has(`${session.id}:${stage}`)) return false
      try {
        if (sessionStorage.getItem(dismissalKey(session.id, stage))) return false
      } catch { /* Storage may be disabled; the in-memory set still prevents repeats. */ }
      announced.current.add(`${session.id}:${stage}`)
      return true
    })
    const empty = fresh.filter(session => session.status === 'empty').map(session => session.id)
    if (empty.length) setEmptyIds(current => [...new Set([...current, ...empty])])
    if (fresh.some(session => feedbackStage(session) === 'processing')) setProgressOpen(true)
    if (fresh.length) {
      clearNotice()
    }
  }, [clearNotice])

  useEffect(() => { announce(imports) }, [imports, announce])

  const submitted = (session: JDImportSession) => {
    // Explicit submissions/retries can reuse the same import ID.
    for (const stage of ['empty', 'processing'] as const) {
      announced.current.delete(`${session.id}:${stage}`)
      try { sessionStorage.removeItem(dismissalKey(session.id, stage)) } catch { /* Optional persistence. */ }
    }
    clearNotice()
    announce([session])
  }
  const dismiss = () => {
    const stage = emptyIds.length ? 'empty' : 'processing'
    for (const id of emptyIds.length ? emptyIds : pending.map(session => session.id)) {
      try { sessionStorage.setItem(dismissalKey(id, stage), '1') } catch { /* Keep the in-memory acknowledgement. */ }
    }
    if (emptyIds.length) setEmptyIds([])
    else setProgressOpen(false)
  }

  return {
    emptyCount: emptyIds.length,
    pendingCount: pending.length,
    queuedCount: pending.filter(session => session.status === 'queued').length,
    runningCount: pending.filter(session => session.status === 'running').length,
    visible: emptyIds.length > 0 || progressOpen && pending.length > 0,
    openProgress: () => setProgressOpen(true), submitted, dismiss,
  }
}

export function JDImportNotice({ emptyCount, queuedCount, runningCount, onDismiss, onAdd }: { emptyCount: number; queuedCount: number; runningCount: number; onDismiss: () => void; onAdd: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const element = dialog.current!
    element.showModal()
    return () => element.close()
  }, [])

  const empty = emptyCount > 0
  const Icon = empty ? AlertCircle : LoaderCircle
  return <dialog ref={dialog} className="jd-import-dialog" role={empty ? 'alertdialog' : 'dialog'} aria-labelledby="jd-import-notice-title" aria-describedby="jd-import-notice-description" onCancel={event => { event.preventDefault(); onDismiss() }}>
    <header>
      <span className="jd-import-notice-icon"><Icon className={empty ? undefined : 'spin'} size={22} aria-hidden="true" /></span>
      <button className="icon-button" type="button" aria-label="关闭提醒" onClick={onDismiss}><X size={19} /></button>
    </header>
    <h2 id="jd-import-notice-title">{empty ? '未识别到有效 JD' : '正在识别岗位内容'}</h2>
    <p id="jd-import-notice-description">{empty
      ? `${emptyCount > 1 ? `有 ${emptyCount} 份材料` : '这份材料'}没有可识别的招聘职责或要求，未添加到 JD 样本，也不会计入市场画像。请重新粘贴包含岗位职责或任职要求的招聘内容。`
      : '系统正在从原始文本中查找岗位，导航、广告和无关内容会被忽略。关闭此窗口后，后台仍会继续识别，结果会自动显示。'}</p>
    {!empty && <p className="jd-import-progress-status" role="status">{queuedCount} 份排队中 · {runningCount} 份正在识别</p>}
    <footer>
      <button className="button" type="button" autoFocus onClick={onDismiss}>{empty ? '知道了' : '后台继续'}</button>
      {empty && <button className="button button-primary" type="button" onClick={() => { onDismiss(); onAdd() }}>重新粘贴</button>}
    </footer>
  </dialog>
}
