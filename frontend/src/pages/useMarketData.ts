import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { jobPilotAPI, type JobDescription, type JDImportSession, type MarketProfile } from '../api'

const emptyProfile: MarketProfile = { included_jd_count: 0, required_jd_count: 10, complete: false, ability_grading_pending_count: 0, ability_grading_failed_count: 0, abilities: [] }

export function marketPollDelay(items: JobDescription[], imports: JDImportSession[], profile: MarketProfile): number | null {
  const delays: number[] = []
  for (const item of items) {
    const pending = item.analysis?.has_pending ?? (item.job_status === 'queued' || item.job_status === 'running' || (!item.ability_review?.blocked && item.ability_review?.pending_count > 0) || (!item.ability_grading?.paused && ['queued', 'running'].includes(item.ability_grading?.status)))
    if (pending) delays.push(Math.max(2000, item.analysis?.next_poll_ms || 2000))
  }
  for (const item of imports) if (item.has_pending) delays.push(Math.max(2000, item.next_poll_ms || 2000))
  if (profile.ability_grading_pending_count > 0 && !delays.length && items.every(item => !item.analysis)) delays.push(2000)
  return delays.length ? Math.min(...delays) : null
}

export function useMarketData(query: string, status: JobDescription['status'] | 'all') {
  const [records, setRecords] = useState<Record<string, JobDescription>>({})
  const [profile, setProfile] = useState(emptyProfile)
  const [imports, setImports] = useState<JDImportSession[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const sequence = useRef(0)
  const reload = useCallback(async (showLoading = true) => {
    const request = ++sequence.current
    if (showLoading) setLoading(true)
    try {
      const [all, nextProfile, nextImports] = await Promise.all([jobPilotAPI.listJDs(), jobPilotAPI.getMarketProfile(), jobPilotAPI.listJDImports()])
      if (request !== sequence.current) return
      setRecords(Object.fromEntries(all.map(item => [item.id, item])))
      setProfile(nextProfile)
      setImports(nextImports)
      setError('')
    } catch (e) {
      if (request === sequence.current) setError(e instanceof Error ? e.message : '读取市场画像失败')
    } finally {
      if (request === sequence.current) setLoading(false)
    }
  }, [])
  useEffect(() => { void reload(); return () => { sequence.current++ } }, [reload])

  const all = useMemo(() => Object.values(records), [records])
  const items = useMemo(() => {
    const search = query.trim().toLocaleLowerCase()
    return all.filter(item => (status === "all" || item.status === status) && (!search || [item.title, item.company, item.primary_category, item.secondary_category, ...(item.ability_mentions ?? []).map(ability => ability.name)].join(" ").toLocaleLowerCase().includes(search)))
  }, [all, query, status])
  const delay = marketPollDelay(all, imports, profile)
  useEffect(() => {
    if (delay === null) return
    let stopped = false
    let timer: number
    const tick = async () => { await reload(false); if (!stopped) timer = window.setTimeout(tick, delay) }
    timer = window.setTimeout(tick, delay)
    return () => { stopped = true; window.clearTimeout(timer) }
  }, [reload, delay])
  useEffect(() => {
    const refresh = () => { if (document.visibilityState !== 'hidden') void reload(false) }
    window.addEventListener('focus', refresh)
    window.addEventListener('jobpilot:agent-change', refresh)
    document.addEventListener('visibilitychange', refresh)
    return () => { window.removeEventListener('focus', refresh); window.removeEventListener('jobpilot:agent-change', refresh); document.removeEventListener('visibilitychange', refresh) }
  }, [reload])

  const upsert = (item: JobDescription) => { sequence.current++; setRecords(current => ({ ...current, [item.id]: item })) }
  const remove = (id: string) => { sequence.current++; setRecords(current => Object.fromEntries(Object.entries(current).filter(([key]) => key !== id))) }
  return { items, all, profile, imports, loading, error, setError, reload, upsert, remove }
}
