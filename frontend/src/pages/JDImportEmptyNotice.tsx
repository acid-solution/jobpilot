import { useCallback, useEffect, useRef, useState } from 'react'
import { AlertCircle, X } from 'lucide-react'
import type { JDImportSession } from '../api'

const dismissalKey = (id: string) => `jobpilot:jd-import:empty-dismissed:${id}`

export function useJDImportFeedback(imports: JDImportSession[], clearNotice: () => void) {
  const announced = useRef(new Set<string>())
  const [emptyIds, setEmptyIds] = useState<string[]>([])

  const announce = useCallback((sessions: JDImportSession[]) => {
    const ids = sessions.filter(session => {
      if (session.status !== 'empty' || announced.current.has(session.id)) return false
      try {
        if (sessionStorage.getItem(dismissalKey(session.id))) return false
      } catch { /* Storage may be disabled; the in-memory set still prevents repeats. */ }
      announced.current.add(session.id)
      return true
    }).map(session => session.id)
    if (ids.length) {
      setEmptyIds(current => [...new Set([...current, ...ids])])
      clearNotice()
    }
  }, [clearNotice])

  useEffect(() => { announce(imports) }, [imports, announce])

  const submitted = (session: JDImportSession) => {
    // An explicit resubmission deserves feedback even if the server reused an empty import.
    announced.current.delete(session.id)
    try { sessionStorage.removeItem(dismissalKey(session.id)) } catch { /* Optional persistence. */ }
    announce([session])
  }
  const dismiss = () => {
    for (const id of emptyIds) {
      try { sessionStorage.setItem(dismissalKey(id), '1') } catch { /* Keep the in-memory acknowledgement. */ }
    }
    setEmptyIds([])
  }

  return { count: emptyIds.length, submitted, dismiss }
}

export function JDImportEmptyNotice({ count, onDismiss, onAdd }: { count: number; onDismiss: () => void; onAdd: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const element = dialog.current!
    element.showModal()
    return () => element.close()
  }, [])

  return <dialog ref={dialog} className="jd-import-empty-dialog" role="alertdialog" aria-labelledby="jd-empty-title" aria-describedby="jd-empty-description" onCancel={event => { event.preventDefault(); onDismiss() }}>
    <header>
      <span className="jd-import-empty-icon"><AlertCircle size={22} aria-hidden="true" /></span>
      <button className="icon-button" type="button" aria-label="关闭提醒" onClick={onDismiss}><X size={19} /></button>
    </header>
    <h2 id="jd-empty-title">未识别到有效 JD</h2>
    <p id="jd-empty-description">{count > 1 ? `有 ${count} 份材料` : '这份材料'}没有可识别的招聘职责或要求，未添加到 JD 样本，也不会计入市场画像。请重新粘贴包含岗位职责或任职要求的招聘内容。</p>
    <footer>
      <button className="button" type="button" autoFocus onClick={onDismiss}>知道了</button>
      <button className="button button-primary" type="button" onClick={() => { onDismiss(); onAdd() }}>重新粘贴</button>
    </footer>
  </dialog>
}
