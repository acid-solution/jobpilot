import { useEffect, useState } from 'react'
import { memoryAPI, type MemorySnapshot } from '../api'
import { memoryLabels, memoryText } from './AgentMemoryPanel'

export function GenerationPreferences({ purpose, basis, adjustment }: { purpose: 'recommendations' | 'route'; basis?: MemorySnapshot; adjustment?: string }) {
 const [current, setCurrent] = useState<MemorySnapshot>()
 const [error, setError] = useState('')
 useEffect(() => { let live=true; const load=()=>{void memoryAPI.list().then(v=>{if(live){setCurrent(v);setError('')}}).catch(()=>{if(live)setError('偏好读取暂时失败；生成时后端会重新校验。')})};load();window.addEventListener('jobpilot:memory-change',load);return()=>{live=false;window.removeEventListener('jobpilot:memory-change',load)} }, [])
 const applicable=(snapshot?:MemorySnapshot)=>snapshot?.memories.filter(m=>purpose==='recommendations'?m.key.startsWith('project.'):m.key!=='answer.detail'&&m.key!=='project.excluded_types')??[]
 return <details className="project-status-banner generation-preferences"><summary>生成偏好{applicable(current).length?` · ${applicable(current).length} 项已确认`:''}</summary><p>以下偏好只用于下一次主动生成，不改变已有项目、选择或路线；本次明确调整意见优先。</p>{applicable(current).map(m=><p key={m.id}><strong>{memoryLabels[m.key]}：</strong>{memoryText(m.value)}</p>)}{current&&!applicable(current).length&&<p>当前没有适用于此功能的已保存偏好。</p>}{error&&<p role="alert">{error}</p>}{basis&&<div><strong>本结果生成时采用的偏好</strong>{applicable(basis).map(m=><p key={m.id}>{memoryLabels[m.key]}：{memoryText(m.value)}</p>)}{adjustment&&<p>本次调整／例外：{adjustment}</p>}</div>}</details>
}
