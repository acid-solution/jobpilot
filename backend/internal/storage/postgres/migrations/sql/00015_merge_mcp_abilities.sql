-- +goose Up
UPDATE abilities
SET name = 'MCP 开发',
    aliases = '["MCP","Model Context Protocol","MCP Server","MCP Client","MCP Server/Client","MCP 服务端","MCP 客户端","MCP Server 开发","MCP Client 开发","MCP 服务端开发","MCP 客户端开发"]'::jsonb,
    definition = '基于 Model Context Protocol 开发和集成服务端或客户端能力，包括工具与资源暴露、能力发现、协议调用、参数校验、错误处理及连接治理。',
    normalized_name = 'mcp开发',
    is_active = TRUE
WHERE code = 'ability-079';

UPDATE ability_levels SET description = '未学习 MCP 开发'
WHERE ability_id = '21000000-0000-0000-0000-000000000079' AND level = 0;
UPDATE ability_levels SET description = '了解 MCP、Server、Client、工具和资源的基本职责与交互过程'
WHERE ability_id = '21000000-0000-0000-0000-000000000079' AND level = 1;
UPDATE ability_levels SET description = '能参考示例实现简单 MCP Server 或连接 MCP Server 完成基础工具调用'
WHERE ability_id = '21000000-0000-0000-0000-000000000079' AND level = 2;
UPDATE ability_levels SET description = '能独立开发 MCP Server 或 Client，处理工具 Schema、参数校验、能力发现、调用结果和常见异常'
WHERE ability_id = '21000000-0000-0000-0000-000000000079' AND level = 3;
UPDATE ability_levels SET description = '能解决多服务连接、权限、生命周期、并发、重试和兼容性问题，并验证可靠性与性能取舍'
WHERE ability_id = '21000000-0000-0000-0000-000000000079' AND level = 4;
UPDATE ability_levels SET description = '能设计可复用的 MCP 接入基础设施、协议治理、安全体系和测试规范，并指导复杂集成'
WHERE ability_id = '21000000-0000-0000-0000-000000000079' AND level = 5;

-- When both old abilities occur in one requirement, keep one option. The
-- requirement evidence already preserves whether the JD mentioned Server,
-- Client, or both.
DELETE FROM job_description_ability_requirement_options client_option
WHERE client_option.ability_id = '21000000-0000-0000-0000-000000000080'
  AND EXISTS (
      SELECT 1
      FROM job_description_ability_requirement_options server_option
      WHERE server_option.requirement_id = client_option.requirement_id
        AND server_option.ability_id = '21000000-0000-0000-0000-000000000079'
  );

UPDATE job_description_ability_requirement_options
SET ability_id = '21000000-0000-0000-0000-000000000079'
WHERE ability_id = '21000000-0000-0000-0000-000000000080';

-- Keep requirement contracts internally valid until the queued v8 analysis
-- atomically replaces them.
WITH option_counts AS (
    SELECT requirement_id, COUNT(*)::INTEGER AS option_count
    FROM job_description_ability_requirement_options
    GROUP BY requirement_id
)
UPDATE job_description_ability_requirements requirement
SET operator = CASE WHEN counts.option_count = 1 THEN 'single' ELSE requirement.operator END,
    required_count = CASE
        WHEN counts.option_count = 1 THEN 1
        WHEN requirement.required_count > counts.option_count THEN counts.option_count
        ELSE requirement.required_count
    END
FROM option_counts counts
WHERE counts.requirement_id = requirement.id
  AND (requirement.required_count > counts.option_count
       OR (counts.option_count = 1 AND requirement.operator <> 'single'));

-- Rebuild the legacy/public flattened relation without leaving two MCP rows.
INSERT INTO job_description_abilities (
    job_description_id, ability_id, raw_name, evidence, required_level, created_at
)
SELECT job_description_id,
       '21000000-0000-0000-0000-000000000079',
       raw_name, evidence, required_level, created_at
FROM job_description_abilities
WHERE ability_id = '21000000-0000-0000-0000-000000000080'
ON CONFLICT (job_description_id, ability_id, evidence) DO NOTHING;

DELETE FROM job_description_abilities
WHERE ability_id = '21000000-0000-0000-0000-000000000080';

UPDATE ability_review_requests
SET resolved_ability_id = '21000000-0000-0000-0000-000000000079'
WHERE resolved_ability_id = '21000000-0000-0000-0000-000000000080';

UPDATE abilities
SET is_active = FALSE
WHERE code = 'ability-080';

-- Re-run affected valid JDs under the unified MCP semantics. Existing results
-- remain visible until the replacement succeeds.
UPDATE analysis_jobs job
SET status = 'queued', attempts = 0, next_attempt_at = NOW(),
    locked_at = NULL, lease_token = NULL, heartbeat_at = NULL,
    lease_expires_at = NULL, last_error = NULL,
    preserve_previous_result = TRUE, updated_at = NOW()
FROM job_descriptions jd
WHERE job.job_description_id = jd.id
  AND job.job_type = 'jd_analysis'
  AND job.status = 'succeeded'
  AND jd.validation_status = 'valid'
  AND EXISTS (
      SELECT 1
      FROM job_description_ability_requirements requirement
      JOIN job_description_ability_requirement_options option
        ON option.requirement_id = requirement.id
      WHERE requirement.job_description_id = jd.id
        AND option.ability_id = '21000000-0000-0000-0000-000000000079'
  );

-- +goose Down
UPDATE abilities
SET name = 'MCP Server 开发',
    aliases = '["MCP Server","MCP服务端","MCP 服务端开发"]'::jsonb,
    definition = '基于 Model Context Protocol 设计、实现并维护向模型暴露工具或资源的服务端。',
    normalized_name = 'mcpserver开发'
WHERE code = 'ability-079';

UPDATE abilities SET is_active = TRUE WHERE code = 'ability-080';
