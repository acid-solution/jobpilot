export type PageId =
  | 'dashboard'
  | 'market'
  | 'profile'
  | 'projects'
  | 'gaps'
  | 'learning'
  | 'settings'

export interface NavItem {
  id: PageId
  shortLabel: string
  label: string
}
