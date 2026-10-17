-- +goose Up
-- These are recall signals, never exact aliases or automatic identity rules.
UPDATE abilities SET include_signals=include_signals ||
 '["API开发中的接口设计、契约设计与前后端分离接口组织"]'::jsonb
 WHERE name='软件设计' AND source='seed';
UPDATE abilities SET include_signals=include_signals ||
 '["数通知识、数据通信基础及协议交互分析"]'::jsonb
 WHERE name='计算机网络' AND source='seed';
UPDATE abilities SET include_signals=include_signals ||
 '["AI接口调用在原文明确指大语言模型API时可作为范围依据"]'::jsonb,
 exclude_signals=exclude_signals || '["泛指AI或AI接口调用不能自动推定为大语言模型，需结合原文具体对象判断"]'::jsonb
 WHERE name='大模型基础' AND source='seed';
UPDATE job_specialties SET exclude_signals=exclude_signals ||
 '["仅探索AI在业务中的应用、嵌入AI能力或开发功能原型，未体现任务规划、工具执行或自主任务闭环，不属于Agent应用"]'::jsonb
 WHERE code='ai-agent-application';

-- +goose Down
UPDATE abilities SET include_signals=include_signals - 'API开发中的接口设计、契约设计与前后端分离接口组织'
 WHERE name='软件设计' AND source='seed';
UPDATE abilities SET include_signals=include_signals - '数通知识、数据通信基础及协议交互分析'
 WHERE name='计算机网络' AND source='seed';
UPDATE abilities SET include_signals=include_signals - 'AI接口调用在原文明确指大语言模型API时可作为范围依据',
 exclude_signals=exclude_signals - '泛指AI或AI接口调用不能自动推定为大语言模型，需结合原文具体对象判断'
 WHERE name='大模型基础' AND source='seed';
UPDATE job_specialties SET exclude_signals=exclude_signals - '仅探索AI在业务中的应用、嵌入AI能力或开发功能原型，未体现任务规划、工具执行或自主任务闭环，不属于Agent应用'
 WHERE code='ai-agent-application';
