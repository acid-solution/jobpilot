-- +goose Up
CREATE TABLE job_categories (
    id UUID PRIMARY KEY,
    code VARCHAR(60) NOT NULL UNIQUE,
    name VARCHAR(100) NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE job_specialties (
    id UUID PRIMARY KEY,
    category_id UUID NOT NULL REFERENCES job_categories(id),
    code VARCHAR(80) NOT NULL UNIQUE,
    name VARCHAR(100) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (category_id, name)
);

INSERT INTO job_categories (id, code, name, sort_order) VALUES
('10000000-0000-0000-0000-000000000001', 'backend', '后端开发', 1),
('10000000-0000-0000-0000-000000000002', 'frontend', '前端开发', 2),
('10000000-0000-0000-0000-000000000003', 'client', '客户端开发', 3),
('10000000-0000-0000-0000-000000000004', 'ai-application', 'AI 应用开发', 4),
('10000000-0000-0000-0000-000000000005', 'data', '数据开发', 5),
('10000000-0000-0000-0000-000000000006', 'testing', '测试开发', 6),
('10000000-0000-0000-0000-000000000007', 'infrastructure', '基础软件与云平台开发', 7),
('10000000-0000-0000-0000-000000000008', 'embedded', '嵌入式开发', 8),
('10000000-0000-0000-0000-000000000009', 'game', '游戏开发', 9);

INSERT INTO job_specialties (id, category_id, code, name, sort_order) VALUES
('11000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000001', 'backend-business', '业务后端', 1),
('11000000-0000-0000-0000-000000000002', '10000000-0000-0000-0000-000000000001', 'backend-framework-middleware', '服务框架与中间件', 2),
('11000000-0000-0000-0000-000000000003', '10000000-0000-0000-0000-000000000001', 'backend-developer-platform', '开发者工具与平台后端', 3),
('11000000-0000-0000-0000-000000000004', '10000000-0000-0000-0000-000000000002', 'frontend-web-miniapp', 'Web 与小程序前端', 1),
('11000000-0000-0000-0000-000000000005', '10000000-0000-0000-0000-000000000002', 'frontend-engineering', '前端工程化与组件平台', 2),
('11000000-0000-0000-0000-000000000006', '10000000-0000-0000-0000-000000000002', 'frontend-visualization-graphics', '可视化与图形前端', 3),
('11000000-0000-0000-0000-000000000007', '10000000-0000-0000-0000-000000000003', 'client-mobile', '移动客户端', 1),
('11000000-0000-0000-0000-000000000008', '10000000-0000-0000-0000-000000000003', 'client-desktop', '桌面客户端', 2),
('11000000-0000-0000-0000-000000000009', '10000000-0000-0000-0000-000000000003', 'client-cross-platform', '跨端客户端', 3),
('11000000-0000-0000-0000-000000000010', '10000000-0000-0000-0000-000000000004', 'ai-rag-knowledge-base', 'RAG 与知识库应用', 1),
('11000000-0000-0000-0000-000000000011', '10000000-0000-0000-0000-000000000004', 'ai-agent-application', 'Agent 应用', 2),
('11000000-0000-0000-0000-000000000012', '10000000-0000-0000-0000-000000000004', 'ai-application-platform', 'AI 应用平台', 3),
('11000000-0000-0000-0000-000000000013', '10000000-0000-0000-0000-000000000005', 'data-offline-warehouse', '离线数据与数仓', 1),
('11000000-0000-0000-0000-000000000014', '10000000-0000-0000-0000-000000000005', 'data-realtime', '实时数据处理', 2),
('11000000-0000-0000-0000-000000000015', '10000000-0000-0000-0000-000000000005', 'data-platform-governance', '数据平台与治理', 3),
('11000000-0000-0000-0000-000000000016', '10000000-0000-0000-0000-000000000006', 'testing-automation', '自动化测试', 1),
('11000000-0000-0000-0000-000000000017', '10000000-0000-0000-0000-000000000006', 'testing-performance-stability', '性能与稳定性测试', 2),
('11000000-0000-0000-0000-000000000018', '10000000-0000-0000-0000-000000000006', 'testing-tools-platform', '测试工具与平台', 3),
('11000000-0000-0000-0000-000000000019', '10000000-0000-0000-0000-000000000007', 'infra-database-storage', '数据库与存储研发', 1),
('11000000-0000-0000-0000-000000000020', '10000000-0000-0000-0000-000000000007', 'infra-os-compiler-runtime', '操作系统与编译运行时', 2),
('11000000-0000-0000-0000-000000000021', '10000000-0000-0000-0000-000000000007', 'infra-cloud', '云平台与基础设施', 3),
('11000000-0000-0000-0000-000000000022', '10000000-0000-0000-0000-000000000007', 'infra-ai', 'AI 基础设施', 4),
('11000000-0000-0000-0000-000000000023', '10000000-0000-0000-0000-000000000008', 'embedded-firmware-driver', '固件与驱动', 1),
('11000000-0000-0000-0000-000000000024', '10000000-0000-0000-0000-000000000008', 'embedded-system', '嵌入式系统', 2),
('11000000-0000-0000-0000-000000000025', '10000000-0000-0000-0000-000000000008', 'embedded-device-application', '设备应用', 3),
('11000000-0000-0000-0000-000000000026', '10000000-0000-0000-0000-000000000009', 'game-client', '游戏客户端', 1),
('11000000-0000-0000-0000-000000000027', '10000000-0000-0000-0000-000000000009', 'game-server', '游戏服务端', 2),
('11000000-0000-0000-0000-000000000028', '10000000-0000-0000-0000-000000000009', 'game-engine-tools', '游戏引擎与工具', 3);

ALTER TABLE job_targets
    ADD COLUMN catalog_status VARCHAR(30) NOT NULL DEFAULT 'reselection_required'
        CHECK (catalog_status IN ('valid', 'reselection_required'));

CREATE TABLE target_directions (
	 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    target_id UUID NOT NULL REFERENCES job_targets(id) ON DELETE CASCADE,
    category_id UUID NOT NULL REFERENCES job_categories(id),
    specialty_id UUID REFERENCES job_specialties(id),
    sort_order INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX target_directions_specialty_unique
    ON target_directions (target_id, specialty_id)
    WHERE specialty_id IS NOT NULL;

CREATE UNIQUE INDEX target_directions_category_unique
    ON target_directions (target_id, category_id)
    WHERE specialty_id IS NULL;

INSERT INTO target_directions (target_id, category_id, specialty_id, sort_order)
SELECT target.id, category.id, specialty.id, direction.ordinality::INTEGER
FROM job_targets target
CROSS JOIN LATERAL jsonb_array_elements(target.directions) WITH ORDINALITY AS direction(value, ordinality)
JOIN job_categories category ON category.name = direction.value ->> 'category'
LEFT JOIN job_specialties specialty
  ON specialty.category_id = category.id
 AND specialty.name = NULLIF(direction.value ->> 'specialty', '')
WHERE COALESCE(direction.value ->> 'specialty', '') = '' OR specialty.id IS NOT NULL
ON CONFLICT DO NOTHING;

UPDATE job_targets target
SET catalog_status = CASE
    WHEN jsonb_array_length(target.directions) > 0
     AND jsonb_array_length(target.directions) = (
        SELECT COUNT(*) FROM target_directions direction WHERE direction.target_id = target.id
     )
    THEN 'valid'
    ELSE 'reselection_required'
END;

-- +goose Down
DROP TABLE IF EXISTS target_directions;
ALTER TABLE job_targets DROP COLUMN IF EXISTS catalog_status;
DROP TABLE IF EXISTS job_specialties;
DROP TABLE IF EXISTS job_categories;
