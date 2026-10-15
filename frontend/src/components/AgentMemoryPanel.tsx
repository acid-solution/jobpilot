import { useEffect, useState } from 'react'
import { X } from 'lucide-react'
import { memoryAPI, type AgentMemory, type MemoryProposal } from '../api'

export const memoryLabels: Record<string, string> = { 'project.languages': '项目语言偏好', 'project.excluded_types': '排除的项目类型', 'project.architecture': '项目架构偏好', 'learning.style': '学习方式偏好', 'answer.detail': '回答详略偏好' }
export function memoryText(value: string | string[] | undefined) { return Array.isArray(value) ? value.join('、') : value ?? '' }
const listKeys = new Set(['project.languages', 'project.excluded_types'])

export function AgentMemoryPanel({ onClose, onChange }: { onClose: () => void; onChange: () => void }) {
 const [items, setItems] = useState<AgentMemory[]>([])
 const [forgotten, setForgotten] = useState<AgentMemory[]>([])
 const [key, setKey] = useState('project.languages')
 const [value, setValue] = useState('')
 const [editing, setEditing] = useState<AgentMemory>()
 const [forgetting, setForgetting] = useState<AgentMemory>()
 const [busy, setBusy] = useState(false)
 const [error, setError] = useState('')
 const reload = async () => { const v=await memoryAPI.list();setItems(v.memories);setForgotten(v.forgotten??[]) }
 useEffect(() => { let live = true; memoryAPI.list().then(v => { if (live) {setItems(v.memories);setForgotten(v.forgotten??[])} }).catch(e => { if (live) setError(String(e)) }); return () => { live = false } }, [])
 const save = async () => {
  setBusy(true); setError('')
  try { await memoryAPI.save(key, listKeys.has(key) ? value.split(/[,，、\n]/).map(s => s.trim()).filter(Boolean) : value.trim(), editing??items.find(m=>m.key===key)??forgotten.find(m=>m.key===key)); setEditing(undefined); setValue(''); await reload(); onChange() }
  catch (e) { setError(e instanceof Error ? e.message : '保存失败') } finally { setBusy(false) }
 }
 const forget = async () => {
  if (!forgetting) return
  setBusy(true); setError('')
  try { await memoryAPI.forget(forgetting); setForgetting(undefined); await reload(); onChange() }
  catch (e) { setError(e instanceof Error ? e.message : '遗忘失败') } finally { setBusy(false) }
 }
 return <section className="conversation-panel agent-memory-panel" aria-label="助手记忆">
  <div className="conversation-panel-header"><h2>已确认的偏好</h2><button type="button" className="icon-button" onClick={onClose} aria-label="关闭记忆面板"><X size={18} /></button></div>
  <p>只影响之后的对话与主动生成，不改动已有项目和路线。</p>
  <div className="agent-memory-list">{items.length === 0 && <p>尚未保存偏好。</p>}{items.map(m => <article className="operation-card" key={m.id}>
   <strong>{memoryLabels[m.key] ?? m.key}</strong><p>{memoryText(m.value)}</p><small>{m.scope === 'global' ? '全部求职目标' : '当前求职目标'} · 修订 {m.revision}</small>
   <details><summary>查看来源</summary><p>{m.source_quote || '你在记忆面板主动保存'}</p><small>{new Date(m.updated_at).toLocaleString('zh-CN')}</small></details>
   <div className="operation-actions"><button type="button" className="button" disabled={busy} onClick={() => { setEditing(m); setKey(m.key); setValue(Array.isArray(m.value) ? m.value.join('，') : m.value) }}>编辑</button><button type="button" className="button" disabled={busy} onClick={() => setForgetting(m)}>遗忘</button></div>
  </article>)}</div>
  <form className="agent-memory-editor" onSubmit={e => { e.preventDefault(); void save() }}>
   <label>偏好类型<select value={key} disabled={!!editing} onChange={e => { setKey(e.target.value); setValue('') }}>{Object.entries(memoryLabels).map(([v, label]) => <option value={v} key={v}>{label}</option>)}</select></label>
   <label>{listKeys.has(key) ? '内容（多项用逗号分隔）' : '内容'}<textarea value={value} onChange={e => setValue(e.target.value)} maxLength={400} placeholder="例如：优先实践，再补充原理" /></label>
   <small>{key === 'answer.detail' ? '适用于全部求职目标' : '仅适用于当前求职目标'}</small>
   <div className="operation-actions"><button className="button button-primary" disabled={busy || !value.trim()}>确认保存</button>{editing && <button type="button" className="button" onClick={() => { setEditing(undefined); setValue('') }}>取消编辑</button>}</div>
  </form>
  {forgetting && <div className="delete-confirm" role="alertdialog" aria-modal="true" aria-label="确认遗忘"><strong>遗忘“{memoryLabels[forgetting.key]}”？</strong><p>保留聊天原文，但{forgetting.scope === 'global' ? '所有目标' : '当前目标'}此前的聊天和摘要将不再送给模型。其他已保存偏好与最新业务资料仍可使用。未确认的旧业务动作需重新提出。</p><div><button className="button button-danger" type="button" disabled={busy} onClick={() => void forget()}>确认遗忘</button><button className="button" type="button" onClick={() => setForgetting(undefined)}>取消</button></div></div>}
  {error && <p role="alert">{error}</p>}
 </section>
}

export function MemorySuggestion({ proposal, onChange }: { proposal: MemoryProposal; onChange: () => void }) {
 const [busy, setBusy] = useState(false), [error, setError] = useState(''), [editing, setEditing] = useState(false), [value, setValue] = useState(memoryText(proposal.value))
 const resolve = async (approve: boolean) => {
  setBusy(true); setError('')
  try {
   if (approve && editing) {
    await memoryAPI.resolve(proposal, true, listKeys.has(proposal.key) ? value.split(/[,，、\n]/).map(v => v.trim()).filter(Boolean) : value)
   } else await memoryAPI.resolve(proposal, approve)
   onChange()
  } catch (e) { setError(e instanceof Error ? e.message : '处理失败') } finally { setBusy(false) }
 }
 return <article className="operation-card agent-memory-suggestion"><span>记忆建议 · 尚未保存</span><strong>{memoryLabels[proposal.key]}</strong>{proposal.before && <p>原来：{memoryText(proposal.before)}</p>}{editing ? <textarea value={value} onChange={e => setValue(e.target.value)} maxLength={400} /> : <p>{memoryText(proposal.value)}</p>}<small>{proposal.scope === 'global' ? '适用于全部目标' : '仅适用于当前目标'}</small><details><summary>来自你的表述</summary><blockquote>{proposal.source_quote}</blockquote></details><div className="operation-actions"><button type="button" className="button button-primary" disabled={busy} onClick={() => void resolve(true)}>确认保存</button><button type="button" className="button" disabled={busy} onClick={() => setEditing(!editing)}>编辑</button><button type="button" className="button" disabled={busy} onClick={() => void resolve(false)}>不保存</button></div>{error && <p role="alert">{error}</p>}</article>
}
