import {
  type PointerEvent as ReactPointerEvent,
  useEffect,
  useRef,
  useState,
} from 'react'
import { ArrowUp, Bot, History, Minus, ScanLine, Sparkles, SquarePen, Trash2 } from 'lucide-react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { agentAPI, type AgentActionRecord, type AgentConversationRecord, type AgentMessageRecord, type AgentStreamEvent } from '../api'

interface AgentWidgetProps {
  currentPage: string
  currentTarget: string
  targetRevision: string
  nextStepPrompt: string
}

interface WindowPosition {
  x: number
  y: number
}

interface DragState {
  pointerId: number
  startX: number
  startY: number
  originX: number
  originY: number
  maxX: number
  maxY: number
}

function AgentMarkdown({ content }: { content: string }) {
  return <div className="agent-markdown"><Markdown remarkPlugins={[remarkGfm]} skipHtml components={{
    a: ({ children, href }) => <a href={href} target="_blank" rel="noopener noreferrer">{children}</a>,
    img: ({ alt }) => <span>{alt ? `[图片：${alt}]` : '[图片]'}</span>,
    table: ({ children }) => <div className="agent-table-scroll"><table>{children}</table></div>,
  }}>{content}</Markdown></div>
}

type AgentCitation = NonNullable<AgentStreamEvent['citation']>

function AgentSources({ citations }: { citations: AgentCitation[] }) {
  if (citations.length === 0) return null
  return <details className="agent-citations">
    <summary>查看检索来源（{citations.length} 条）</summary>
    {citations.map((citation, index) => <details className="agent-citation" key={`${citation.source_id}-${index}`}>
      <summary>{citation.source_type === 'jd' ? '岗位 JD' : citation.source_type === 'resume' ? '简历' : '经历'} · 来源 {index + 1}</summary>
      <blockquote>{citation.quote}</blockquote>
      <span className="agent-source-id">来源 ID：{citation.source_id}</span>
    </details>)}
  </details>
}

function messagesWithSources(messages: AgentMessageRecord[]) {
  const visible: (AgentMessageRecord & { citations: AgentCitation[] })[] = []
  let citations: AgentCitation[] = []
  for (const message of messages) {
    if (message.role === 'tool') {
      try {
        const record = JSON.parse(message.content) as { type?: string; citations?: AgentCitation[] }
        if (record.type === 'source_citations' && Array.isArray(record.citations)) {
          for (const citation of record.citations) {
            if (typeof citation?.source_id === 'string' && typeof citation.source_type === 'string' && typeof citation.quote === 'string'
              && !citations.some((item) => item.source_id === citation.source_id && item.quote === citation.quote)) citations.push(citation)
          }
        }
      } catch { /* Older tool records need not contain source metadata. */ }
      continue
    }
    if (message.role === 'user') citations = []
    visible.push({ ...message, citations: message.role === 'assistant' ? citations : [] })
    citations = []
  }
  return visible
}

const readResourceLabels: Record<string, string> = {
  target: '求职目标', job_catalog: '岗位目录', jd_list: 'JD 列表', jd_detail: 'JD 详情',
  market_profile: '市场画像', user_profile: '用户画像', profile_settings: '必要信息', materials: '简历与经历',
  knowledge_gaps: '知识短板', recommendations: '项目推荐', source_search: '资料原文', task_status: '任务状态',
}

export function AgentWidget({ currentPage, currentTarget, targetRevision }: AgentWidgetProps) {
  const [open, setOpen] = useState(true)
  const [historyOpen, setHistoryOpen] = useState(false)
  const [conversations, setConversations] = useState<AgentConversationRecord[]>([])
  const [activeId, setActiveId] = useState('')
  const [activeConversation, setActiveConversation] = useState<AgentConversationRecord | null>(null)
  const [deleteId, setDeleteId] = useState<string | null>(null)
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [liveText, setLiveText] = useState('')
  const [toolStatus, setToolStatus] = useState('')
  const [citations, setCitations] = useState<AgentCitation[]>([])
  const [pendingAction, setPendingAction] = useState<AgentActionRecord | null>(null)
  const [position, setPosition] = useState<WindowPosition | null>(null)
  const windowRef = useRef<HTMLElement | null>(null)
  const dragRef = useRef<DragState | null>(null)
  const threadRef = useRef<HTMLDivElement | null>(null)
  const scopeEpoch = useRef(0)

  useEffect(() => {
    if (currentTarget === '正在读取……') return
    const epoch = ++scopeEpoch.current
    let cancelled = false
    setError('')
    setActiveId('')
    setActiveConversation(null)
    setPendingAction(null)
    setLiveText('')
    setToolStatus('')
    setCitations([])
    setBusy(false)
    agentAPI.list().then(async (items) => {
      if (cancelled || epoch !== scopeEpoch.current) return
      const available = items.length ? items : [await agentAPI.create(currentPage)]
      if (cancelled || epoch !== scopeEpoch.current) return
      setConversations(available)
      setActiveId(available[0].id)
    }).catch((failure: unknown) => { if (!cancelled && epoch === scopeEpoch.current) setError(failure instanceof Error ? failure.message : '读取对话失败') })
    return () => { cancelled = true; scopeEpoch.current++ }
  }, [targetRevision])

  useEffect(() => {
    if (!activeId) return
    let cancelled = false
    const epoch = scopeEpoch.current
    agentAPI.get(activeId).then((value) => {
      if (cancelled || epoch !== scopeEpoch.current) return
      setActiveConversation(value)
      setPendingAction(value.pending_action ?? null)
    }).catch((failure: unknown) => { if (!cancelled && epoch === scopeEpoch.current) setError(failure instanceof Error ? failure.message : '读取对话失败') })
    return () => { cancelled = true }
  }, [activeId, targetRevision])

  useEffect(() => { threadRef.current?.scrollTo({ top: threadRef.current.scrollHeight }) }, [activeConversation, liveText, pendingAction])

  const refresh = async (id: string, epoch = scopeEpoch.current) => {
    const [items, detail] = await Promise.all([agentAPI.list(), agentAPI.get(id)])
    if (epoch !== scopeEpoch.current) return
    setConversations(items)
    setActiveConversation(detail)
    setPendingAction(detail.pending_action ?? null)
  }

  const createConversation = async () => {
    if (busy) return
    const epoch = scopeEpoch.current
    try {
      setError('')
      const conversation = await agentAPI.create(currentPage)
      if (epoch !== scopeEpoch.current) return
      setConversations((current) => [conversation, ...current])
      setActiveId(conversation.id)
      setActiveConversation(conversation)
      setPendingAction(conversation.pending_action ?? null)
      setHistoryOpen(false)
      setDeleteId(null)
      setDraft('')
      setLiveText('')
      setToolStatus('')
      setCitations([])
    } catch (failure) { if (epoch === scopeEpoch.current) setError(failure instanceof Error ? failure.message : '新建对话失败') }
  }

  const removeConversation = async () => {
    if (!deleteId || busy) return
    const epoch = scopeEpoch.current
    try {
      await agentAPI.delete(deleteId)
      if (epoch !== scopeEpoch.current) return
      const remaining = conversations.filter((conversation) => conversation.id !== deleteId)
      setDeleteId(null)
      if (remaining.length === 0) {
        setConversations([])
        setActiveId('')
        setActiveConversation(null)
        await createConversation()
        return
      }
      setConversations(remaining)
      if (activeId === deleteId) setActiveId(remaining[0].id)
    } catch (failure) { if (epoch === scopeEpoch.current) setError(failure instanceof Error ? failure.message : '删除对话失败') }
  }

  const onStreamEvent = (event: AgentStreamEvent, epoch: number) => {
    if (epoch !== scopeEpoch.current) return
    if (event.type === 'delta') setLiveText((current) => current + (event.text ?? ''))
    if (event.type === 'tool') setToolStatus(event.tool === 'read_jobpilot'
      ? `已查询${readResourceLabels[event.text ?? ''] ?? '业务资料'}`
      : event.text === '已执行' ? '操作已完成' : event.text === '已取消' ? '操作已取消' : '正在处理业务操作')
    if (event.type === 'action' && event.action) setPendingAction(event.action)
    if (event.type === 'citation' && event.citation) {
      const citation = event.citation
      setCitations((current) => current.some((item) => item.source_id === citation.source_id && item.quote === citation.quote) ? current : [...current, citation])
    }
    if (event.type === 'tool' && event.text === '已执行') {
      window.dispatchEvent(new Event('jobpilot:agent-change'))
    }
  }
  const sendMessage = async () => {
    const content = draft.trim()
    if (!content || !activeId || busy || pendingAction) return
    const conversationId = activeId
    const epoch = scopeEpoch.current
    setDraft('')
    setBusy(true); setError(''); setLiveText(''); setToolStatus(''); setCitations([])
    setActiveConversation((current) => current ? { ...current, messages: [...(current.messages ?? []), { id: `pending-${Date.now()}`, role: 'user', content, created_at: new Date().toISOString() }] } : current)
    try { await agentAPI.send(conversationId, content, currentPage, (event) => onStreamEvent(event, epoch)); await refresh(conversationId, epoch) }
    catch (failure) {
      if (epoch === scopeEpoch.current) setError(failure instanceof Error ? failure.message : '发送失败')
      await refresh(conversationId, epoch).catch(() => undefined)
    }
    finally { if (epoch === scopeEpoch.current) { setBusy(false); setLiveText(''); setCitations([]) } }
  }
  const resolveAction = async (approve: boolean) => {
    if (!activeId || !pendingAction || busy) return
    const conversationId = activeId
    const epoch = scopeEpoch.current
    setBusy(true); setError(''); setLiveText(''); setToolStatus('')
    try {
      await agentAPI.resolve(conversationId, pendingAction.id, approve, (event) => onStreamEvent(event, epoch))
      await refresh(conversationId, epoch)
      if (approve && epoch === scopeEpoch.current) window.dispatchEvent(new Event('jobpilot:agent-change'))
    } catch (failure) {
      if (epoch === scopeEpoch.current) setError(failure instanceof Error ? failure.message : '操作失败')
      await refresh(conversationId, epoch).catch(() => undefined)
    }
    finally { if (epoch === scopeEpoch.current) { setBusy(false); setLiveText(''); setCitations([]) } }
  }

  const startDrag = (event: ReactPointerEvent<HTMLDivElement>) => {
    if ((event.target as HTMLElement).closest('button')) return
    if (window.matchMedia('(max-width: 760px)').matches) return

    const element = windowRef.current
    if (!element) return

    const elementRect = element.getBoundingClientRect()
    const originX = elementRect.left
    const originY = elementRect.top

    dragRef.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      originX,
      originY,
      maxX: Math.max(8, window.innerWidth - elementRect.width - 8),
      maxY: Math.max(8, window.innerHeight - elementRect.height - 8),
    }
    event.currentTarget.setPointerCapture(event.pointerId)
  }

  const continueDrag = (event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current
    if (!drag || drag.pointerId !== event.pointerId) return

    setPosition({
      x: Math.min(drag.maxX, Math.max(8, drag.originX + event.clientX - drag.startX)),
      y: Math.min(drag.maxY, Math.max(8, drag.originY + event.clientY - drag.startY)),
    })
  }

  const finishDrag = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (dragRef.current?.pointerId !== event.pointerId) return
    dragRef.current = null
    event.currentTarget.releasePointerCapture(event.pointerId)
  }

  if (!open) {
    return (
      <button className="agent-pet" type="button" onClick={() => setOpen(true)} aria-label="打开求职 Agent">
        <Bot size={23} aria-hidden="true" />
        <span className="agent-pet-status" aria-hidden="true" />
      </button>
    )
  }

  return (
    <section
      className="agent-window"
      aria-label="求职 Agent 浮动窗口"
      ref={windowRef}
      style={position ? { left: position.x, top: position.y, right: 'auto', bottom: 'auto' } : undefined}
    >
      <div
        className="agent-header"
        onPointerDown={startDrag}
        onPointerMove={continueDrag}
        onPointerUp={finishDrag}
        onPointerCancel={finishDrag}
      >
        <div className="agent-identity">
          <span className="agent-avatar" aria-hidden="true"><Bot size={18} /></span>
          <div className="agent-title">
            <strong>{activeConversation?.title ?? '求职 Agent'}</strong>
            <span>{busy ? '正在处理……' : '求职 Agent · 已连接真实资料'}</span>
          </div>
        </div>
        <div className="agent-header-actions">
          <button className="icon-button" type="button" onClick={createConversation} aria-label="新建对话"><SquarePen size={18} /></button>
          <button
            className="icon-button"
            type="button"
            onClick={() => {
              setHistoryOpen((current) => !current)
              setDeleteId(null)
            }}
            aria-label="查看对话列表"
            aria-expanded={historyOpen}
          >
            <History size={18} />
          </button>
          <button className="icon-button" type="button" onClick={() => setOpen(false)} aria-label="收起助手"><Minus size={18} /></button>
        </div>
      </div>

      <div className="agent-context">
        <ScanLine size={14} aria-hidden="true" />
        <span>{currentPage} · {currentTarget}</span>
      </div>

      <div className="agent-thread" ref={threadRef}>
        {messagesWithSources(activeConversation?.messages ?? []).map((message) => (
          <div className={`agent-message agent-message-${message.role}`} key={message.id}>
            {message.role === 'assistant' && <span className="message-avatar" aria-hidden="true"><Sparkles size={14} /></span>}
            <div className="message-bubble">
              {message.role === 'assistant' ? <AgentMarkdown content={message.content} /> : <p className="agent-user-content">{message.content}</p>}
              <AgentSources citations={message.citations} />
            </div>
          </div>
        ))}
        {liveText && <div className="agent-message agent-message-assistant"><span className="message-avatar" aria-hidden="true"><Sparkles size={14} /></span><div className="message-bubble"><AgentMarkdown content={liveText} /></div></div>}
        {toolStatus && <div className="tool-note" role="status">{toolStatus}</div>}
        <AgentSources citations={citations} />
        {pendingAction && (
          <div className="operation-card">
            <span>等待你的确认</span>
            <strong>{pendingAction.summary}</strong>
            <p>以下是本次要提交的完整参数。确认后只执行这一项操作。</p>
            <pre className="agent-action-arguments">{JSON.stringify(pendingAction.arguments, null, 2)}</pre>
            <div className="operation-actions">
              <button className="button button-primary" type="button" disabled={busy} onClick={() => void resolveAction(true)}>确认并执行</button>
              <button className="button" type="button" disabled={busy} onClick={() => void resolveAction(false)}>取消</button>
            </div>
          </div>
        )}
        {error && <div className="operation-result" role="alert">{error}</div>}
      </div>

      <div className="agent-composer">
        <div className="composer-row">
          <textarea
            value={draft}
            disabled={busy || !!pendingAction}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.shiftKey) {
                event.preventDefault()
                sendMessage()
              }
            }}
            aria-label="给求职 Agent 发送消息"
            placeholder="问问当前页面、画像或推荐结果……"
          />
          <button className="send-button" type="button" disabled={busy || !!pendingAction || !activeId} onClick={() => void sendMessage()} aria-label="发送消息"><ArrowUp size={18} /></button>
        </div>
        <div className="composer-note">新信息不会自动写入画像，保存前会让你确认。</div>
      </div>

      {historyOpen && (
        <section className="conversation-panel" aria-label="对话列表">
          <div className="conversation-panel-header">
            <h2>对话</h2>
            <button className="button button-primary" type="button" disabled={busy} onClick={() => void createConversation()}>新建对话</button>
          </div>
          <div className="conversation-list">
            {conversations.map((conversation) => (
              <div
                className={`conversation-row ${conversation.id === activeConversation?.id ? 'is-current' : ''}`}
                key={conversation.id}
              >
                <button
                  className="conversation-select"
                  type="button"
                  disabled={busy}
                  onClick={() => {
                    setActiveId(conversation.id)
                    setActiveConversation(null)
                    setHistoryOpen(false)
                    setDeleteId(null)
                    setPendingAction(null)
                    setError('')
                    setLiveText('')
                    setToolStatus('')
                    setCitations([])
                  }}
                >
                  <strong>{conversation.title}</strong>
                  <span>{new Date(conversation.updated_at).toLocaleString('zh-CN')}</span>
                </button>
                <button
                  className="icon-button conversation-delete"
                  type="button"
                  disabled={busy}
                  onClick={() => setDeleteId(conversation.id)}
                  aria-label={`删除对话：${conversation.title}`}
                >
                  <Trash2 size={16} />
                </button>
              </div>
            ))}
          </div>
          {deleteId && (
            <div className="delete-confirm" role="alertdialog" aria-modal="true" aria-labelledby="delete-title">
              <p id="delete-title">删除对话“{conversations.find((item) => item.id === deleteId)?.title}”？</p>
              <div>
                <button className="button" type="button" onClick={() => setDeleteId(null)}>取消</button>
                <button className="button button-danger" type="button" onClick={() => void removeConversation()}>删除</button>
              </div>
            </div>
          )}
        </section>
      )}
    </section>
  )
}
