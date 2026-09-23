-- +goose Up
UPDATE abilities
SET is_active = FALSE
WHERE code = 'ability-073';

INSERT INTO abilities (
    id, category_id, code, name, aliases, sort_order, is_active,
    definition, normalized_name, source
) VALUES
('21000000-0000-0000-0000-000000000078', '20000000-0000-0000-0000-000000000007',
 'ability-078', 'Function Calling', '["Tool Calling","函数调用","工具调用"]'::jsonb, 81, TRUE,
 '让大模型按照结构化参数选择并调用外部函数或工具，并正确处理调用结果。', 'functioncalling', 'seed'),
('21000000-0000-0000-0000-000000000079', '20000000-0000-0000-0000-000000000007',
 'ability-079', 'MCP Server 开发', '["MCP Server","MCP服务端","MCP 服务端开发"]'::jsonb, 82, TRUE,
 '基于 Model Context Protocol 设计、实现并维护向模型暴露工具或资源的服务端。', 'mcpserver开发', 'seed'),
('21000000-0000-0000-0000-000000000080', '20000000-0000-0000-0000-000000000007',
 'ability-080', 'MCP Client 开发', '["MCP Client","MCP客户端","MCP 客户端开发"]'::jsonb, 83, TRUE,
 '基于 Model Context Protocol 发现、连接并调用服务端工具或资源，并处理协议交互。', 'mcpclient开发', 'seed')
ON CONFLICT DO NOTHING;

INSERT INTO ability_levels (ability_id, level, description) VALUES
('21000000-0000-0000-0000-000000000078', 0, '未学习 Function Calling'),
('21000000-0000-0000-0000-000000000078', 1, '了解模型调用函数或工具的基本过程和结构化参数用途'),
('21000000-0000-0000-0000-000000000078', 2, '能参考示例声明工具并完成一次基础调用闭环'),
('21000000-0000-0000-0000-000000000078', 3, '能独立设计工具接口、校验参数并处理调用结果与常见异常'),
('21000000-0000-0000-0000-000000000078', 4, '能解决复杂工具选择、并发调用、重试与安全控制问题，并验证效果和成本取舍'),
('21000000-0000-0000-0000-000000000078', 5, '能设计可复用的工具调用基础设施、接口规范和评测体系，并指导复杂应用建设'),
('21000000-0000-0000-0000-000000000079', 0, '未学习 MCP Server 开发'),
('21000000-0000-0000-0000-000000000079', 1, '了解 MCP Server、工具和资源的基本职责'),
('21000000-0000-0000-0000-000000000079', 2, '能参考示例实现简单 MCP Server 并暴露基础工具'),
('21000000-0000-0000-0000-000000000079', 3, '能独立设计工具 Schema、实现服务端能力并处理参数校验和错误'),
('21000000-0000-0000-0000-000000000079', 4, '能解决复杂权限、生命周期、并发和可观测问题，验证协议实现与业务边界'),
('21000000-0000-0000-0000-000000000079', 5, '能设计可复用的 MCP Server 框架、治理规范和安全体系，并指导复杂服务建设'),
('21000000-0000-0000-0000-000000000080', 0, '未学习 MCP Client 开发'),
('21000000-0000-0000-0000-000000000080', 1, '了解 MCP Client 如何发现并调用服务端工具和资源'),
('21000000-0000-0000-0000-000000000080', 2, '能参考示例连接 MCP Server 并完成基础工具调用'),
('21000000-0000-0000-0000-000000000080', 3, '能独立实现服务发现、能力协商、调用处理和常见异常恢复'),
('21000000-0000-0000-0000-000000000080', 4, '能解决多服务连接、权限、重试和兼容性问题，验证可靠性与性能取舍'),
('21000000-0000-0000-0000-000000000080', 5, '能设计可复用的 MCP Client 接入层、连接治理和测试规范，并指导复杂集成')
ON CONFLICT (ability_id, level) DO NOTHING;

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
      JOIN abilities ability ON ability.id = option.ability_id
      WHERE requirement.job_description_id = jd.id
        AND ability.code = 'ability-073'
  );

-- +goose Down
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
      JOIN abilities ability ON ability.id = option.ability_id
      WHERE requirement.job_description_id = jd.id
        AND ability.code IN ('ability-078','ability-079','ability-080')
  );

UPDATE abilities SET is_active = FALSE WHERE code IN ('ability-078','ability-079','ability-080');
UPDATE abilities SET is_active = TRUE WHERE code = 'ability-073';
