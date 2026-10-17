import { useCallback, useEffect, useRef, useState } from 'react'
import { jobPilotAPI, type ProfilePracticeMode, type ProfilePracticeSession } from '../api'

type Answers = Record<string, string>
type Options = { onChanged?: (session: ProfilePracticeSession) => Promise<void> | void }
const message = (error: unknown) => error instanceof Error ? error.message : '问答操作失败，请稍后重试'
const savedAnswers = (session: ProfilePracticeSession): Answers => Object.fromEntries((session.answers ?? []).map(item => [item.question_id, item.answer]))

export function isResumablePractice(session: ProfilePracticeSession) {
  return session.status !== 'evaluated' || !session.level_updated && (
    session.mode === 'initial' && session.evaluation?.suggested_level !== undefined ||
    session.mode === 'validation' && session.evaluation?.verdict === 'pass'
  )
}

// Both page layouts use the same persisted session protocol. All writes are
// serialized, so an autosave cannot race a submit or overwrite its evaluation.
export function usePracticeSession(options: Options = {}) {
  const [session, setSession] = useState<ProfilePracticeSession | null>(null)
  const [history, setHistory] = useState<ProfilePracticeSession[]>([])
  const [answers, setAnswers] = useState<Answers>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const active = useRef(true)
  const epoch = useRef(0)
  const historySequence = useRef(0)
  const current = useRef(session)
  const drafts = useRef(answers)
  const pending = useRef(false)
  const queue = useRef<Promise<unknown>>(Promise.resolve())
  const callbacks = useRef(options)
  callbacks.current = options
  useEffect(() => {
    active.current = true
    return () => { active.current = false; epoch.current++; historySequence.current++ }
  }, [])

  const clearError = useCallback(() => setError(''), [])
  const reset = useCallback(() => {
    epoch.current++; historySequence.current++; pending.current = false
    current.current = null; drafts.current = {}
    setSession(null); setAnswers({}); setBusy(false); setError('')
  }, [])
  const remember = useCallback((value: ProfilePracticeSession, keepDrafts = true) => {
    current.current = value
    drafts.current = keepDrafts ? { ...savedAnswers(value), ...drafts.current } : savedAnswers(value)
    setSession(value); setAnswers(drafts.current)
    historySequence.current++
    setHistory(items => [value, ...items.filter(item => item.id !== value.id)])
  }, [])
  const resume = useCallback((value: ProfilePracticeSession) => {
    reset(); remember(value, false)
  }, [remember, reset])
  const setAnswer = useCallback((id: string, value: string) => {
    drafts.current = { ...drafts.current, [id]: value }; setAnswers(drafts.current)
  }, [])
  const loadHistory = useCallback(async () => {
    const sequence = ++historySequence.current
    try {
      const response = await jobPilotAPI.listProfileSessions()
      if (active.current && sequence === historySequence.current) setHistory(response.sessions ?? [])
    } catch (e) {
      if (active.current && sequence === historySequence.current) setError(message(e))
    }
  }, [])

  const enqueue = <T,>(action: () => Promise<T>) => {
    const next = queue.current.catch(() => {}).then(action)
    queue.current = next
    return next
  }
  // Explicit actions are gated immediately, before React renders disabled buttons.
  const run = async (action: (valid: () => boolean) => Promise<ProfilePracticeSession | null>, notify: boolean | ((value: ProfilePracticeSession) => boolean) = false) => {
    if (pending.current) return null
    pending.current = true
    const generation = epoch.current
    const valid = () => active.current && generation === epoch.current
    setBusy(true); setError('')
    try {
      const next = await enqueue(() => {
        if (!valid()) return Promise.resolve(null)
        setError('')
        return action(valid)
      })
      if (next && valid() && (typeof notify === 'function' ? notify(next) : notify)) {
        try { await callbacks.current.onChanged?.(next) }
        catch (e) { if (valid()) setError(`问答结果已保存，刷新画像失败：${message(e)}`) }
      }
      return valid() ? next : null
    } catch (e) {
      if (valid()) setError(message(e))
      return null
    } finally {
      if (valid()) { pending.current = false; setBusy(false) }
    }
  }
  const start = (abilityId: string, mode: ProfilePracticeMode) => run(async valid => {
    const { session: next } = await jobPilotAPI.startProfileSession(abilityId, mode)
    if (valid()) remember(next, false)
    return next
  })
  const saveAnswer = async (id: string, value: string) => {
    const original = current.current
    if (!original || original.status === 'evaluated' || !value.trim()) return null
    const generation = epoch.current
    const valid = () => active.current && generation === epoch.current && current.current?.id === original.id
    try {
      return await enqueue(async () => {
        if (!valid() || current.current?.status === 'evaluated') return null
        const { session: next } = await jobPilotAPI.saveProfileAnswer(original.id, id, value.trim())
        if (valid()) remember(next)
        return next
      })
    } catch (e) {
      if (valid()) setError(`回答暂未保存：${message(e)}`)
      return null
    }
  }
  const evaluate = async (value: ProfilePracticeSession, valid: () => boolean) => {
    if (value.status === 'evaluated') return value
    const submitted = value.questions.map(question => ({ question_id: question.id, answer: drafts.current[question.id]?.trim() ?? '' }))
    if (submitted.some(item => !item.answer)) throw new Error('请先回答本轮全部问题')
    const { session: next } = await jobPilotAPI.submitProfileSession(value.id, submitted)
    if (valid()) remember(next)
    return next
  }
  const submit = () => run(async valid => current.current ? evaluate(current.current, valid) : null, true)
  const answerAndContinue = (id: string, value: string) => run(async valid => {
    const original = current.current
    if (!original || original.status === 'evaluated' || !value.trim()) return null
    const { session: next } = await jobPilotAPI.saveProfileAnswer(original.id, id, value.trim())
    if (!valid()) return null
    // Publish the saved answer BEFORE evaluation. A failed model call must leave
    // the fully answered session visible, so retry does not resubmit that answer.
    remember(next)
    return next.questions.every(question => (next.answers ?? []).some(item => item.question_id === question.id && item.answer.trim())) ? evaluate(next, valid) : next
  }, next => next.status === 'evaluated')
  const confirm = () => run(async valid => {
    const original = current.current
    if (!original || original.level_updated || original.mode === 'review' || !isResumablePractice(original) || original.status !== 'evaluated') return null
    const { session: next } = await jobPilotAPI.confirmProfileSession(original.id)
    if (valid()) remember(next)
    return next
  }, true)

  return { session, history, answers, busy, error, clearError, reset, resume, setAnswer, loadHistory, start, saveAnswer, submit, answerAndContinue, confirm }
}
