export interface UsageSummary {
    payer: string;
    call_count: number;
    unknown_usage_count: number;
    unknown_cost_count: number;
    input_tokens: number;
    output_tokens: number;
    cache_hit_tokens: number;
    cache_miss_tokens: number;
    cache_hit_rate: number | null;
    estimated_costs: Record<string, string>;
}
export interface UsageCall {
    id: string;
    provider: string;
    requested_model: string;
    returned_model: string;
    provider_request_id?: string;
    payer: string;
    purpose: string;
    operation_id: string;
    started_at: string;
    duration_ms: number;
    result: string;
    usage_status: string;
    input_tokens: number | null;
    output_tokens: number | null;
    total_tokens: number | null;
    cache_hit_tokens: number | null;
    cache_miss_tokens: number | null;
    currency?: string;
    estimated_cost: string | null;
    legacy: boolean;
    confirmed: boolean;
    price_snapshot?: {
        version: string;
    };
}
export interface UsageOperation {
    id: string;
    purpose: string;
    started_at: string;
    call_count: number;
}
export interface UsagePage<T> {
    items: T[];
    total: number;
    page: number;
    page_size: number;
}
export const usagePurposes: Record<string, string> = { jd_extraction: 'JD 内容提取', jd_classification: 'JD 岗位分类', jd_ability_resolution: 'JD 能力归一化', jd_import: 'JD 材料识别', jd_classification_review: '岗位新增分类审核', jd_analysis: 'JD 分析', jd_normalization: '能力重新归一化', jd_grading: 'JD 能力判级', ability_review: '能力目录审核', ability_alias_review: '能力别名审核', material_analysis: '简历与经历分析', interview: '面试问答', project_recommendation: '项目推荐', learning_route: '学习路线', agent: 'Agent 对话与操作', embedding: '资料向量化', connection_test: '模型连接测试' };
