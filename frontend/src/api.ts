export interface TargetDirection {
  category_id: string
  category: string
  specialty_id?: string
  specialty?: string
}

export interface JobSpecialty {
  id: string
  code: string
  name: string
}

export interface JobCategory {
  id: string
  code: string
  name: string
  specialties: JobSpecialty[]
}

export interface JobTarget {
  id: string
  title: string
  employment_type: 'internship' | 'campus' | 'social'
  graduation_year?: number
  directions: TargetDirection[]
  catalog_status: 'valid' | 'reselection_required'
  created_at: string
  updated_at: string
}

export interface SaveTargetInput {
  employment_type: JobTarget['employment_type']
  graduation_year?: number
  directions: Array<{ category_id: string; specialty_id?: string }>
}

export interface JobDescription {
  id: string
  target_id: string
  title?: string
  company?: string
  status: 'processing' | 'included' | 'reference' | 'excluded' | 'failed'
  primary_category?: string
  secondary_category?: string
  reason?: string
  conditions?: string
  employment_type?: 'internship' | 'campus' | 'social' | 'unknown'
  responsibilities: string[]
  ability_mentions: Array<{ name: string; qualifier?: string; evidence: string; required_level?: number }>
  ability_levels: JDAbilityLevel[]
  analysis_provider?: string
  analysis_model?: string
  analysis_prompt_version?: string
  document_type?: 'job_description' | 'partial_job_description' | 'non_job_description' | 'unreadable'
  validation_status: 'pending' | 'valid' | 'incomplete' | 'invalid'
  validation_reason?: string
  raw_text: string
  job_status: 'queued' | 'running' | 'succeeded' | 'failed'
  created_at: string
  updated_at: string
  job_error_code?: string
	ability_review: {
		pending_count: number
		failed_count: number
		next_attempt_at?: string
		blocked?: boolean
	}
	ability_grading: {
		status: 'not_started' | 'queued' | 'running' | 'succeeded' | 'failed'
		error?: string
	}
}

export interface JDAbilityLevel {
	ability_id: string
	name: string
	level: number
	source: 'explicit' | 'inferred'
	requirement_kind: 'required' | 'preferred' | 'unspecified'
	evidence: string
	reason: string
	confidence: number
}

export interface MarketAbilityEvidence {
  job_description_id: string
  job_title: string
  evidence: string
  qualifier?: string
}

export interface MarketAbilitySummary {
  ability_id: string
  name: string
  category: string
  covered_jd_count: number
  evidences: MarketAbilityEvidence[]
  target_level: number
  level_summary: MarketLevelSummary
  preferred_level_summary: MarketLevelSummary
}

export interface MarketLevelEvidence {
  job_description_id: string
  job_title: string
  level: number
  source: 'explicit' | 'inferred'
  requirement_kind: 'required' | 'preferred' | 'unspecified'
  evidence: string
  reason: string
  confidence: number
}

export interface MarketLevelSummary {
  common_levels: number[]
  recommended_level: number
  sample_count: number
  explicit_count: number
  inferred_count: number
  distribution: Array<{ level: number; count: number }>
  higher_requirement_count: number
  status: 'ready' | 'processing' | 'pending' | 'failed'
  evidences: MarketLevelEvidence[]
}

export interface MarketProfile {
  included_jd_count: number
  required_jd_count: number
  complete: boolean
  ability_grading_pending_count: number
  ability_grading_failed_count: number
  abilities: MarketAbilitySummary[]
}

export type ProfileMaterialType = 'resume' | 'experience'
export type ProfileMaterialStatus = 'draft' | 'processing' | 'ready' | 'failed'
export type ProfilePracticeMode = 'validation' | 'review' | 'initial'

export interface ProfileMaterial {
  id: string
  type: ProfileMaterialType
  title: string
  text: string
  status: ProfileMaterialStatus
  failure_reason?: string
  evidence_count: number
  confirmed_at?: string
  created_at: string
  updated_at: string
}

export interface ProfileLevelDefinition {
  level: number
  description: string
}

export interface ProfileEvidence {
  id: string
  material_id: string
  concept_id: string
  concept_name: string
  level: number
  quote: string
  reason: string
  confidence: number
  created_at: string
}

export interface ProfileCapability {
  concept_id: string
  name: string
  category?: string
  market_level: number
  market_level_ready: boolean
  current_level: number
  assessed: boolean
  level_source: string
  manual_updated_at?: string
  evidence_level: number
  verified_level: number
  status: 'needs_evidence' | 'evidence_backed' | 'verified'
  needs_validation: boolean
  levels: ProfileLevelDefinition[]
  evidence: ProfileEvidence[]
  updated_at?: string
}

export interface UserProfileOverview {
  market_profile_ready: boolean
  material_count: number
  ready_material_count: number
  assessed_count: number
  pending_count: number
  blocking_count: number
  complete: boolean
  capabilities: ProfileCapability[]
}

export interface ProfileQuestion {
  id: string
  prompt: string
  dimension: string
  position: number
  hint?: string
  reference?: string
  explanation?: string
}

export interface ProfileQuestionResult {
  question_id: string
  passed: boolean
  feedback: string
}

export interface ProfilePracticeSession {
  id: string
  concept_id: string
  concept_name: string
  mode: ProfilePracticeMode
  base_level: number
  target_level: number
  status: 'ready' | 'clarifying' | 'evaluated'
  clarification_count: number
  questions: ProfileQuestion[]
  answers?: Array<{ question_id: string; answer: string }>
  evaluation?: {
    passed: boolean
    score: number
    verdict: 'pass' | 'not_yet' | 'uncertain' | 'review'
    suggested_level?: number
    summary: string
    question_results: ProfileQuestionResult[]
  }
  level_updated: boolean
  created_at: string
  completed_at?: string
}

export interface KnowledgeGapEvidence { jd_id: string; jd_title: string; quote: string; level: number; source: 'explicit' | 'inferred'; reason: string }
export interface KnowledgeGapItem {
  id: string
  kind: 'ability' | 'requirement'
  name: string
  ability_id?: string
  current_level?: number
  target_level?: number
  user_source?: string
  user_evidence?: ProfileEvidence[]
  sample_count: number
  required_count?: number
  satisfied_count?: number
  options?: Array<{ name: string; ability_id: string; current_level: number; required_level: number; assessed: boolean; satisfied: boolean }>
  evidences: KnowledgeGapEvidence[]
}
export interface KnowledgeGapReport { target_id: string; gaps: KnowledgeGapItem[]; met: KnowledgeGapItem[]; preferred: KnowledgeGapItem[]; generated_at: string }
export interface KnowledgeGapView {
  report?: KnowledgeGapReport
  stale: boolean
  readiness: { code: string; message: string; included_jd_count: number; pending_review_count: number; failed_review_count: number; pending_grading_count: number; failed_grading_count: number; missing_ability_count: number }
}

export interface ProjectReference {
  full_name: string
  url: string
  description: string
  language: string
  archived: boolean
  checked_at: string
  facts: Array<{ text: string; quote: string; source_url: string }>
  comparison: {
    similarities: string[]
    differences: string[]
    project_advantages: string[]
    project_limits: string[]
    repository_advantages: string[]
    repository_limits: string[]
  }
}
export interface RecommendedProject {
  id: string
  rank: number
  title: string
  summary: string
  audience: string
  problem: string
  shape: string
  scope: string[]
  fit_reasons: string[]
  abilities: string[]
  duration: string
  known_facts: string[]
  assumptions: string[]
  references: ProjectReference[]
  searched_directions: string[]
  no_reference_reason?: string
}
export interface ProjectRecommendationsView {
  readiness: KnowledgeGapView['readiness']
  report?: { id: string; target_title: string; projects: RecommendedProject[]; empty_reason?: string; generated_at: string }
  selected_project_id?: string
  stale: boolean
  job?: {
    id: string
    status: 'queued' | 'running' | 'succeeded' | 'failed'
    phase: 'draft' | 'research' | 'compare'
    draft_count: number
    researched_count: number
    attempts: number
    max_attempts: number
    next_attempt_at?: string
    error_code?: string
    updated_at: string
  }
}

export interface UserProfileSettings {
  existing_experience: string
  weekly_hours?: number
  expected_weeks?: number
  updated_at?: string
}

export interface JDListFilters {
  statuses?: JobDescription['status'][]
  query?: string
  company?: string
  ability?: string
  category?: string
}

export interface JDBatchResult {
  created_count: number
  duplicate_count: number
  invalid_count: number
  items: Array<{
    index: number
    status: 'created' | 'duplicate' | 'invalid'
    jd?: JobDescription
    existing_jd_id?: string
    error_code?: string
    error_message?: string
  }>
}

export interface ModelConfig {
  provider: 'deepseek'
  model: 'deepseek-flash'
  configured: boolean
  key_hint?: string
  updated_at?: string
}

export interface AuthIdentity {
  id: string
  kind: 'email'
  value: string
  verified_at: string
  created_at: string
}

export interface AuthAccount {
  user: {
    id: string
    status: 'active' | 'disabled'
    created_at: string
    updated_at: string
  }
  identities: AuthIdentity[]
}

export interface AuthSession {
  account: AuthAccount
  access_token: string
  token_type: 'Bearer'
  expires_at: string
}

export interface VerificationChallenge {
  challenge_id: string
  expires_at: string
  resend_after: string
}

export interface DevOutboxMessage {
  identifier: string
  code: string
  purpose: 'register' | 'reset'
  created_at: string
}

interface DataResponse<T> {
  data: T
}

interface ErrorResponse {
  error?: {
    code?: string
    message?: string
  }
}

export class APIError extends Error {
  status: number
  code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'APIError'
    this.status = status
    this.code = code
  }
}

const AUTH_CLIENT_ID = 'jobpilot'
let accessToken = ''
let refreshPromise: Promise<AuthSession> | null = null
let authFailureHandler: (() => void) | null = null

async function parseResponse<T>(response: Response): Promise<T> {
  const body = await response.json().catch(() => ({})) as DataResponse<T> & ErrorResponse & T
  if (!response.ok) {
    throw new APIError(
      response.status,
      body.error?.code ?? 'request_failed',
      body.error?.message ?? '请求失败，请稍后重试',
    )
  }
  // 旧后端使用 { data: ... }，重构后端直接返回资源对象。迁移期间兼容两种格式。
  return Object.prototype.hasOwnProperty.call(body, 'data') ? body.data : body
}

async function authRequest<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      ...init?.headers,
    },
  })
  return parseResponse<T>(response)
}

async function refreshSession(): Promise<AuthSession> {
  if (!refreshPromise) {
    refreshPromise = authRequest<AuthSession>('/auth/v1/token/refresh', {
      method: 'POST',
      body: JSON.stringify({ client_id: AUTH_CLIENT_ID }),
    }).then((session) => {
      accessToken = session.access_token
      return session
    }).finally(() => {
      refreshPromise = null
    })
  }
  return refreshPromise
}

async function request<T>(path: string, init?: RequestInit, mayRefresh = true): Promise<T> {
	const headers = new Headers(init?.headers)
	if (!(init?.body instanceof FormData)) headers.set('Content-Type', 'application/json')
	if (accessToken) headers.set('Authorization', `Bearer ${accessToken}`)
  const response = await fetch(path, {
    ...init,
	headers,
  })
  if (response.status === 401 && mayRefresh) {
    try {
      await refreshSession()
      return request<T>(path, init, false)
    } catch {
      accessToken = ''
      authFailureHandler?.()
    }
  }
  return parseResponse<T>(response)
}

export const authAPI = {
  setFailureHandler: (handler: (() => void) | null) => {
    authFailureHandler = handler
  },
  requestVerification: (email: string, purpose: 'register' | 'reset') => authRequest<VerificationChallenge>('/auth/v1/verifications', {
    method: 'POST',
    body: JSON.stringify({ client_id: AUTH_CLIENT_ID, email, purpose }),
  }),
  register: (challengeId: string, code: string, password: string) => authRequest<AuthSession>('/auth/v1/register', {
    method: 'POST',
    body: JSON.stringify({ client_id: AUTH_CLIENT_ID, challenge_id: challengeId, code, password }),
  }).then((session) => {
    accessToken = session.access_token
    return session
  }),
  login: (email: string, password: string) => authRequest<AuthSession>('/auth/v1/login', {
    method: 'POST',
    body: JSON.stringify({ client_id: AUTH_CLIENT_ID, email, password }),
  }).then((session) => {
    accessToken = session.access_token
    return session
  }),
  refresh: refreshSession,
  logout: async () => {
    try {
      await authRequest<void>('/auth/v1/logout', {
        method: 'POST',
        body: JSON.stringify({ client_id: AUTH_CLIENT_ID }),
      })
    } finally {
      accessToken = ''
    }
  },
  resetPassword: async (challengeId: string, code: string, newPassword: string) => {
    await authRequest<void>('/auth/v1/password/reset', {
      method: 'POST',
      body: JSON.stringify({
        client_id: AUTH_CLIENT_ID,
        challenge_id: challengeId,
        code,
        new_password: newPassword,
      }),
    })
    accessToken = ''
  },
  devOutbox: (email: string) => authRequest<DevOutboxMessage[]>(`/dev/outbox?identifier=${encodeURIComponent(email)}`),
}

export const jobPilotAPI = {
	getJobCatalog: () => request<JobCategory[]>('/api/v1/catalog/job-directions'),
  getCurrentTarget: () => request<JobTarget>('/api/v1/targets/current'),
  saveCurrentTarget: (input: SaveTargetInput) => request<JobTarget>('/api/v1/targets/current', {
    method: 'PUT',
    body: JSON.stringify(input),
  }),
	listJDs: (filters: JDListFilters = {}) => {
		const params = new URLSearchParams()
		filters.statuses?.forEach((status) => params.append('status', status))
		if (filters.query?.trim()) params.set('q', filters.query.trim())
		if (filters.company?.trim()) params.set('company', filters.company.trim())
		if (filters.ability?.trim()) params.set('ability', filters.ability.trim())
		if (filters.category?.trim()) params.set('category', filters.category.trim())
		const query = params.toString()
		return request<JobDescription[]>(`/api/v1/jds${query ? `?${query}` : ''}`).then((items) => Array.isArray(items) ? items : [])
	},
	getMarketProfile: () => request<MarketProfile>('/api/v1/market-profile').then((profile) => ({
		...profile,
		abilities: Array.isArray(profile.abilities) ? profile.abilities : [],
		required_jd_count: profile.required_jd_count > 0 ? profile.required_jd_count : 10,
	})),
  getJD: (id: string) => request<JobDescription>(`/api/v1/jds/${id}`),
  createJD: (rawText: string) => request<JobDescription>('/api/v1/jds', {
    method: 'POST',
    body: JSON.stringify({ raw_text: rawText }),
  }),
	createJDBatch: (rawTexts: string[]) => request<JDBatchResult>('/api/v1/jds/batch', {
		method: 'POST',
		body: JSON.stringify({ raw_texts: rawTexts }),
	}),
	updateJD: (id: string, rawText: string) => request<JobDescription>(`/api/v1/jds/${id}`, {
		method: 'PUT',
		body: JSON.stringify({ raw_text: rawText }),
	}),
	deleteJD: (id: string) => request<void>(`/api/v1/jds/${id}`, { method: 'DELETE' }),
  retryJD: (id: string) => request<JobDescription>(`/api/v1/jds/${id}/retry`, { method: 'POST' }),
	retryAbilityReviews: (id: string) => request<JobDescription>(`/api/v1/jds/${id}/ability-reviews/retry`, { method: 'POST' }),
	retryAbilityGrading: (id: string) => request<JobDescription>(`/api/v1/jds/${id}/ability-grading/retry`, { method: 'POST' }),
  getDeepSeekConfig: () => request<ModelConfig>('/api/v1/model-configs/deepseek'),
  saveDeepSeekConfig: (apiKey: string, model = 'deepseek-flash') => request<ModelConfig>('/api/v1/model-configs/deepseek', {
    method: 'PUT',
    body: JSON.stringify({ api_key: apiKey, model }),
  }),
  testDeepSeekConfig: (apiKey = '', model = 'deepseek-flash') => request<{ connected: boolean }>('/api/v1/model-configs/deepseek/test', {
    method: 'POST',
    body: JSON.stringify({ api_key: apiKey, model }),
  }),
  deleteDeepSeekConfig: () => request<void>('/api/v1/model-configs/deepseek', { method: 'DELETE' }),
  getUserProfile: () => request<{ profile: UserProfileOverview }>('/api/v1/profile'),
  setProfileCapabilityLevel: (id: string, level: number) => request<{ capability: ProfileCapability }>(`/api/v1/profile/capabilities/${id}/level`, { method: 'PUT', body: JSON.stringify({ level }) }),
  getKnowledgeGaps: () => request<KnowledgeGapView>('/api/v1/knowledge-gaps'),
  analyzeKnowledgeGaps: () => request<KnowledgeGapView>('/api/v1/knowledge-gaps/analyze', { method: 'POST' }),
  getProjectRecommendations: () => request<ProjectRecommendationsView>('/api/v1/project-recommendations'),
  generateProjectRecommendations: (adjustment = '') => request<ProjectRecommendationsView>('/api/v1/project-recommendations', { method: 'POST', body: JSON.stringify({ adjustment }) }),
  selectRecommendedProject: (reportId: string, projectId: string) => request<ProjectRecommendationsView>('/api/v1/project-recommendations/selection', { method: 'PUT', body: JSON.stringify({ report_id: reportId, project_id: projectId }) }),
  getProfileSettings: () => request<{ settings: UserProfileSettings }>('/api/v1/profile/settings'),
  saveProfileSettings: (input: { weekly_hours: number; expected_weeks: number; existing_experience: string }) => request<{ settings: UserProfileSettings }>('/api/v1/profile/settings', {
    method: 'PUT',
    body: JSON.stringify(input),
  }),
  listProfileMaterials: () => request<{ materials: ProfileMaterial[] }>('/api/v1/profile/materials'),
  extractProfileMaterial: (file: File) => {
    const body = new FormData()
    body.append('file', file)
    return request<{ filename: string; title: string; text: string }>('/api/v1/profile/materials/extract', {
      method: 'POST',
      body,
    })
  },
  createProfileMaterial: (input: { type: ProfileMaterialType; title: string; text: string }) => request<{ material: ProfileMaterial }>('/api/v1/profile/materials', {
    method: 'POST',
    body: JSON.stringify(input),
  }),
  updateProfileMaterial: (id: string, input: { type: ProfileMaterialType; title: string; text: string }) => request<{ material: ProfileMaterial }>(`/api/v1/profile/materials/${id}`, {
    method: 'PUT',
    body: JSON.stringify(input),
  }),
  deleteProfileMaterial: (id: string) => request<{ deleted: boolean }>(`/api/v1/profile/materials/${id}`, { method: 'DELETE' }),
  confirmProfileMaterial: (id: string) => request<{ material: ProfileMaterial }>(`/api/v1/profile/materials/${id}/confirm`, { method: 'POST' }),
  startProfileSession: (conceptId: string, mode: ProfilePracticeMode) => request<{ session: ProfilePracticeSession }>('/api/v1/profile/sessions', {
    method: 'POST',
    body: JSON.stringify({ concept_id: conceptId, mode }),
  }),
  listProfileSessions: () => request<{ sessions: ProfilePracticeSession[] }>('/api/v1/profile/sessions'),
  getProfileSession: (id: string) => request<{ session: ProfilePracticeSession }>(`/api/v1/profile/sessions/${id}`),
  saveProfileAnswer: (id: string, questionId: string, answer: string) => request<{ session: ProfilePracticeSession }>(`/api/v1/profile/sessions/${id}/answers`, { method: 'PUT', body: JSON.stringify({ question_id: questionId, answer }) }),
  confirmProfileSession: (id: string) => request<{ session: ProfilePracticeSession }>(`/api/v1/profile/sessions/${id}/confirm`, { method: 'POST' }),
  submitProfileSession: (id: string, answers: Array<{ question_id: string; answer: string }>) => request<{ session: ProfilePracticeSession }>(`/api/v1/profile/sessions/${id}/submit`, {
    method: 'POST',
    body: JSON.stringify({ answers }),
  }),
}

export interface AgentMessageRecord { id: string; role: 'user' | 'assistant' | 'tool'; content: string; created_at: string }
export interface AgentActionRecord { id: string; kind: string; arguments: Record<string, unknown>; summary: string; status: string }
export interface AgentConversationRecord { id: string; title: string; status: 'idle' | 'running' | 'awaiting_confirmation'; updated_at: string; messages?: AgentMessageRecord[]; pending_action?: AgentActionRecord }
export interface AgentStreamEvent { type: 'delta' | 'tool' | 'citation' | 'action' | 'done' | 'error'; text?: string; tool?: string; action?: AgentActionRecord; citation?: { source_type: string; source_id: string; quote: string }; code?: string }

async function streamAgent(path: string, body: unknown, onEvent: (event: AgentStreamEvent) => void, mayRefresh = true): Promise<void> {
  const headers = new Headers({ 'Content-Type': 'application/json' })
  if (accessToken) headers.set('Authorization', `Bearer ${accessToken}`)
  const controller = new AbortController()
  const timeout = window.setTimeout(() => controller.abort(), 210_000)
  let idleExpired = false
  let idleTimeout = window.setTimeout(() => { idleExpired = true; controller.abort() }, 60_000)
  try {
    const response = await fetch(path, { method: 'POST', headers, body: JSON.stringify(body), signal: controller.signal })
    if (response.status === 401 && mayRefresh) {
      await refreshSession()
      return streamAgent(path, body, onEvent, false)
    }
    if (!response.ok) { await parseResponse(response); return }
    if (!response.body) throw new Error('当前连接不支持流式响应')
    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    let finished = false
    while (true) {
      const { value, done } = await reader.read()
      if (value?.length) {
        window.clearTimeout(idleTimeout)
        idleTimeout = window.setTimeout(() => { idleExpired = true; controller.abort() }, 60_000)
      }
      buffer += decoder.decode(value, { stream: !done })
      const frames = buffer.split(/\r?\n\r?\n/)
      buffer = frames.pop() ?? ''
      for (const frame of frames) {
        const line = frame.split(/\r?\n/).find((part) => part.startsWith('data: '))
        if (!line) continue
        const event = JSON.parse(line.slice(6)) as AgentStreamEvent
        onEvent(event)
        if (event.type === 'done') finished = true
        if (event.type === 'error') throw new Error(event.text ?? 'Agent 执行失败')
      }
      if (done) break
    }
    if (!finished) throw new Error('Agent 连接中断，操作状态请刷新对话核对')
  } catch (error) {
    if (idleExpired) throw new Error('Agent 已超过 60 秒没有新进展，请刷新对话核对状态后重试')
    if (controller.signal.aborted) throw new Error('Agent 等待超时，请刷新对话核对处理状态后重试')
    throw error
  } finally {
    window.clearTimeout(timeout)
    window.clearTimeout(idleTimeout)
  }
}

export const agentAPI = {
  list: () => request<AgentConversationRecord[]>('/api/v1/agent/conversations'),
  get: (id: string) => request<AgentConversationRecord>(`/api/v1/agent/conversations/${id}`),
  create: (page: string) => request<AgentConversationRecord>('/api/v1/agent/conversations', { method: 'POST', body: JSON.stringify({ page }) }),
  delete: (id: string) => request<void>(`/api/v1/agent/conversations/${id}`, { method: 'DELETE' }),
  send: (id: string, content: string, page: string, onEvent: (event: AgentStreamEvent) => void) => streamAgent(`/api/v1/agent/conversations/${id}/messages`, { content, page }, onEvent),
  resolve: (id: string, actionId: string, approve: boolean, onEvent: (event: AgentStreamEvent) => void) => streamAgent(`/api/v1/agent/conversations/${id}/actions/${actionId}`, { approve }, onEvent),
}
