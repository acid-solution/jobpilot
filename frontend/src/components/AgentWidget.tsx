import {
  type PointerEvent as ReactPointerEvent,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { ArrowUp, Bot, History, Minus, ScanLine, Sparkles, SquarePen, Trash2 } from 'lucide-react'
import type { ChatMessage, Conversation } from '../types'

const STORAGE_KEY = 'jobpilot-conversations-v2'

interface AgentWidgetProps {
  currentPage: string
  currentTarget: string
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

const OLD_WELCOME = '这是一个新对话。你可以询问当前页面、画像或推荐结果。'

function nextStepMessage(prompt: string): ChatMessage {
  return {
    id: `next-step-${Date.now()}`,
    role: 'assistant',
    content: prompt,
    toolNote: '已根据当前求职目标和画像进度生成 · 未修改资料',
  }
}

function loadConversations(prompt: string): Conversation[] {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (!stored) return [initialConversation(prompt)]
    const parsed = JSON.parse(stored) as Conversation[]
    if (!Array.isArray(parsed) || parsed.length === 0) return [initialConversation(prompt)]

    return parsed.map((conversation) => ({
      ...conversation,
      messages: conversation.messages.map((message) =>
        message.content === OLD_WELCOME ||
        (message.role === 'assistant' &&
          (typeof message.content !== 'string' || message.content.trim() === ''))
          ? {
              ...message,
              content: prompt,
              toolNote: '已根据当前求职目标和画像进度生成 · 未修改资料',
            }
          : message,
      ),
    }))
  } catch {
    return [initialConversation(prompt)]
  }
}

function initialConversation(prompt: string): Conversation {
  return {
    id: 'conversation-start',
    title: '下一步做什么',
    updatedLabel: '刚刚',
    messages: [{ ...nextStepMessage(prompt), id: 'conversation-start-next-step' }],
  }
}

function newConversation(prompt: string): Conversation {
  const id = `conversation-${Date.now()}`
  return {
    id,
    title: '新对话',
    updatedLabel: '刚刚',
    messages: [{ ...nextStepMessage(prompt), id: `${id}-next-step` }],
  }
}

export function AgentWidget({ currentPage, currentTarget, nextStepPrompt }: AgentWidgetProps) {
  const [open, setOpen] = useState(true)
  const [historyOpen, setHistoryOpen] = useState(false)
  const [conversations, setConversations] = useState(() => loadConversations(nextStepPrompt))
  const [activeId, setActiveId] = useState(() => loadConversations(nextStepPrompt)[0]?.id ?? '')
  const [deleteId, setDeleteId] = useState<string | null>(null)
  const [draft, setDraft] = useState('')
  const [operationVisible, setOperationVisible] = useState(false)
  const [operationResult, setOperationResult] = useState('')
  const [position, setPosition] = useState<WindowPosition | null>(null)
  const windowRef = useRef<HTMLElement | null>(null)
  const dragRef = useRef<DragState | null>(null)

  const activeConversation = useMemo(
    () => conversations.find((conversation) => conversation.id === activeId) ?? conversations[0],
    [activeId, conversations],
  )

  useEffect(() => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(conversations))
  }, [conversations])

  useEffect(() => {
    setConversations((current) => current.map((conversation) => ({
      ...conversation,
      messages: conversation.messages.map((message, index) =>
        index === 0 && message.role === 'assistant' && message.id.includes('next-step')
          ? {
              ...message,
              content: nextStepPrompt,
              toolNote: '已根据当前求职目标和真实画像进度生成 · 未修改资料',
            }
          : message,
      ),
    })))
  }, [nextStepPrompt])

  const createConversation = () => {
    const conversation = newConversation(nextStepPrompt)
    setConversations((current) => [conversation, ...current])
    setActiveId(conversation.id)
    setHistoryOpen(false)
    setDeleteId(null)
    setDraft('')
    setOperationVisible(false)
    setOperationResult('')
  }

  const removeConversation = () => {
    if (!deleteId) return

    const remaining = conversations.filter((conversation) => conversation.id !== deleteId)
    if (remaining.length === 0) {
      const replacement = newConversation(nextStepPrompt)
      setConversations([replacement])
      setActiveId(replacement.id)
    } else {
      setConversations(remaining)
      if (activeId === deleteId) setActiveId(remaining[0].id)
    }
    setDeleteId(null)
  }

  const appendMessage = (conversationId: string, message: ChatMessage) => {
    setConversations((current) =>
      current.map((conversation) =>
        conversation.id === conversationId
          ? {
              ...conversation,
              updatedLabel: '刚刚',
              messages: [...conversation.messages, message],
            }
          : conversation,
      ),
    )
  }

  const sendMessage = () => {
    const content = draft.trim()
    if (!content || !activeConversation) return

    const userMessage: ChatMessage = {
      id: `message-${Date.now()}`,
      role: 'user',
      content,
    }
    const assistantMessage: ChatMessage = {
      id: `message-${Date.now()}-reply`,
      role: 'assistant',
      content: '当前页面已经接入真实画像进度，但 Agent 对话与工具调用后端还没有接通。这条消息已保存在本机对话中。',
      toolNote: '未调用模型或修改业务资料',
    }

    if (activeConversation.title === '新对话') {
      setConversations((current) =>
        current.map((conversation) =>
          conversation.id === activeConversation.id
            ? { ...conversation, title: content.slice(0, 18) }
            : conversation,
        ),
      )
    }
    appendMessage(activeConversation.id, userMessage)
    window.setTimeout(() => appendMessage(activeConversation.id, assistantMessage), 250)
    setDraft('')
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
            <span>求职 Agent · 页面进度已接入</span>
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

      <div className="agent-thread">
        {activeConversation?.messages.map((message) => (
          <div className={`agent-message agent-message-${message.role}`} key={message.id}>
            {message.role === 'assistant' && <span className="message-avatar" aria-hidden="true"><Sparkles size={14} /></span>}
            <div className="message-bubble">
              <p>{message.content}</p>
              {message.toolNote && <div className="tool-note">{message.toolNote}</div>}
            </div>
          </div>
        ))}

        {operationVisible && activeConversation?.id === 'jd-relevance' && (
          <div className="operation-card">
            <span>等待你的确认</span>
            <strong>保存 D 公司完整 JD</strong>
            <p>保存原文并开始后台分析，完成前暂不计入市场画像。</p>
            <div className="operation-actions">
              <button
                className="button button-primary"
                type="button"
                onClick={() => {
                  setOperationVisible(false)
                  setOperationResult('已确认：JD 已保存，后台分析已开始。')
                }}
              >
                确认并执行
              </button>
              <button
                className="button"
                type="button"
                onClick={() => {
                  setOperationVisible(false)
                  setOperationResult('已取消，没有修改任何资料。')
                }}
              >
                取消
              </button>
            </div>
          </div>
        )}
        <div className="operation-result" aria-live="polite">{operationResult}</div>
      </div>

      <div className="agent-composer">
        <div className="composer-row">
          <textarea
            value={draft}
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
          <button className="send-button" type="button" onClick={sendMessage} aria-label="发送消息"><ArrowUp size={18} /></button>
        </div>
        <div className="composer-note">新信息不会自动写入画像，保存前会让你确认。</div>
      </div>

      {historyOpen && (
        <section className="conversation-panel" aria-label="对话列表">
          <div className="conversation-panel-header">
            <h2>对话</h2>
            <button className="button button-primary" type="button" onClick={createConversation}>新建对话</button>
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
                  onClick={() => {
                    setActiveId(conversation.id)
                    setHistoryOpen(false)
                    setDeleteId(null)
                    setOperationVisible(conversation.id === 'jd-relevance')
                    setOperationResult('')
                  }}
                >
                  <strong>{conversation.title}</strong>
                  <span>{conversation.updatedLabel}</span>
                </button>
                <button
                  className="icon-button conversation-delete"
                  type="button"
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
                <button className="button button-danger" type="button" onClick={removeConversation}>删除</button>
              </div>
            </div>
          )}
        </section>
      )}
    </section>
  )
}
