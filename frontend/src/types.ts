export type PageId =
  | 'dashboard'
  | 'market'
  | 'profile'
  | 'projects'
  | 'gaps'
  | 'settings'

export interface NavItem {
  id: PageId
  shortLabel: string
  label: string
}

export interface PreparationStep {
  id: string
  shortLabel: string
  title: string
  description: string
  state: string
  complete: boolean
}

export interface ChatMessage {
  id: string
  role: 'assistant' | 'user'
  content: string
  toolNote?: string
}

export interface Conversation {
  id: string
  title: string
  updatedLabel: string
  messages: ChatMessage[]
}
