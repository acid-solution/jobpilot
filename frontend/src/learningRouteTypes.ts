import type { KnowledgeGapReport, KnowledgeGapView, MemorySnapshot } from './api'

export interface LearningPlanConfig { mode: 'sequence' | 'calendar'; weekly_minutes: number; start_date?: string; learning_days?: number[]; daily_minutes?: number }
export interface LearningRouteConfig { project: LearningPlanConfig; knowledge: LearningPlanConfig; paths?: Array<{ gap_id: string; ability_ids: string[] }> }
export interface LearningTask { id: string; title: string; description: string; completion_criteria: string; estimate_minutes: number; scheduled_date?: string; depends_on?: string[] }
export interface LearningStage { title: string; outcome: string; tasks: LearningTask[] }
export interface LearningPlan { role: 'project' | 'knowledge'; title: string; description: string; objective: string; stages: LearningStage[]; path_reasons?: string[]; selected_paths?: Array<{gap_id: string; ability_ids: string[]}> }
export interface LearningRouteVersion { preferences?: MemorySnapshot; preference_adjustment?: string; id: string; route_id: string; version_no: number; revision: number; content_hash: string; config: LearningRouteConfig; plans: LearningPlan[]; generated_at: string }
export interface LearningRouteReceipt { import_id: string; goal_id: string; source_route_id: string; source_version: number; content_digest: string; plans: Array<{ role: string; plan_id: string; plan_version_id: string; version_no: number }> }
export interface LearningRouteView {
 goal_title: string
 project_title?: string
 readiness: KnowledgeGapView['readiness']; knowledge_summary?: KnowledgeGapReport; version?: LearningRouteVersion
 job?: { id: string; status: string; attempts: number; error_code?: string; error_message?: string; updated_at: string }
 import?: { id: string; version_id: string; revision: number; status: string; error_code?: string; receipt?: LearningRouteReceipt }
 knowledge_needs_update: boolean; project_needs_review: boolean; warnings: string[]; import_enabled: boolean; studyflow_url?: string
}
