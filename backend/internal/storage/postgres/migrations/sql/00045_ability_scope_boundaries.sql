-- +goose Up
ALTER TABLE abilities
 ADD COLUMN include_signals JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(include_signals)='array'),
 ADD COLUMN exclude_signals JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(exclude_signals)='array'),
 ADD COLUMN confused_with JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(confused_with)='array');
ALTER TABLE job_description_ability_requirement_options
 ADD COLUMN normalization_relation VARCHAR(24) NOT NULL DEFAULT '',
 ADD COLUMN scope_details JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(scope_details)='array');

-- Give seed candidates a usable definition without changing their level scale.
UPDATE abilities a SET definition=COALESCE(NULLIF(a.definition,''), a.name || '；评估范围参考：' || l.description)
 FROM ability_levels l WHERE l.ability_id=a.id AND l.level=3 AND a.source='seed';
WITH scopes(name,inc,exc) AS (VALUES
 ('计算机网络','["常见协议用途与交互流程，HTTP、SSH、SFTP等协议的理解","连接、超时、传输问题，使用Wireshark等抓包工具进行协议分析","数通指数据通信基础时可作为内容范围"]'::jsonb,'["仅使用SSH登录服务器不能证明协议分析能力","网络服务端编程、协议栈实现需结合网络编程目录判断","不得把所有网络安全产品或运维工具视为同义名称"]'::jsonb),
 ('数据结构与算法','["数据结构、算法与复杂度","常用数据结构的选择和实现"]'::jsonb,'["计算机基础不是本能力的同义名称，也包括操作系统和网络"]'::jsonb),
 ('操作系统','["进程、线程、调度、内存、文件系统与操作系统原理"]'::jsonb,'["使用Linux命令不能直接证明操作系统原理能力"]'::jsonb),
 ('Linux','["Linux常用操作、Shell脚本与服务管理","使用SSH、SFTP进行远程登录和文件操作"]'::jsonb,'["理解协议原理需对照计算机网络","内核实现与操作系统原理不能仅凭命令使用确定"]'::jsonb),
 ('软件设计','["业务逻辑抽象、模块设计、前后端分离架构","API设计、接口契约、数据建模与UML表达"]'::jsonb,'["不要把所有语言、框架或数据库产品合并到软件设计","UML等表达方式通常是设计依据，不自动创建细项评分"]'::jsonb),
 ('网络编程','["网络服务端、连接管理、Socket、协议实现与网络I/O编程"]'::jsonb,'["仅了解常见协议或使用抓包工具不代表网络编程"]'::jsonb),
 ('MySQL','["MySQL使用、SQL、索引、事务、查询优化与数据建模"]'::jsonb,'["PostgreSQL、Redis是不同产品，不是MySQL细项或别名","数据库原理不能仅凭产品使用推定"]'::jsonb),
 ('PostgreSQL','["PostgreSQL使用、SQL、索引、事务与数据建模"]'::jsonb,'["MySQL、Redis是不同产品，不应合并"]'::jsonb),
 ('Redis','["Redis数据结构、缓存、持久化与一致性处理"]'::jsonb,'["MySQL、PostgreSQL是不同产品，不应合并"]'::jsonb),
 ('大模型基础','["大语言模型LLM基本原理、常见应用场景与接口调用","结合原文判断大模型在具体业务领域的基础应用认知"]'::jsonb,'["完整机器学习能力不等同于大模型基础","具体框架、RAG、MCP、Function Calling须对照各自目录，不强行合并"]'::jsonb)
)
UPDATE abilities a SET include_signals=s.inc,exclude_signals=s.exc FROM scopes s WHERE a.name=s.name AND a.source='seed';
WITH edges(name,other,rule) AS (VALUES
 ('计算机网络','Linux','协议交互与问题分析侧重计算机网络；远程登录和系统操作侧重Linux'),
 ('计算机网络','网络编程','协议理解与分析不同于网络服务端或协议实现代码开发'),
 ('操作系统','Linux','操作系统原理不同于Linux命令与运维实践'),
 ('MySQL','数据库原理','数据库产品实践不同于通用数据库原理'),
 ('大模型基础','RAG','原理与常见应用认知不同于检索增强链路的工程实践')
), data AS (
 SELECT a.id,jsonb_agg(jsonb_build_object('ability_code',b.code,'distinction',e.rule)) value
 FROM edges e JOIN abilities a ON a.name=e.name JOIN abilities b ON b.name=e.other GROUP BY a.id
) UPDATE abilities a SET confused_with=data.value FROM data WHERE a.id=data.id;

-- Remove unreviewed labels from the public list without issuing any model calls.
UPDATE job_descriptions jd SET ability_mentions=COALESCE((
 SELECT jsonb_agg(confirmed.value ORDER BY confirmed.requirement_order,confirmed.option_order) FROM (
  SELECT DISTINCT ON(a.id,o.evidence)
   jsonb_build_object('name',a.name,'catalog_code',a.code,'qualifier',o.qualifier,'evidence',o.evidence,'required_level',o.required_level) value,
   r.sort_order requirement_order,o.sort_order option_order
  FROM job_description_ability_requirements r JOIN job_description_ability_requirement_options o ON o.requirement_id=r.id
  JOIN abilities a ON a.id=o.ability_id AND a.is_active
  WHERE r.job_description_id=jd.id AND o.resolution_status='resolved'
  ORDER BY a.id,o.evidence,o.required_level DESC NULLS LAST,r.sort_order,o.sort_order
 ) confirmed),'[]'::jsonb)
 WHERE EXISTS(SELECT 1 FROM job_description_ability_requirements r WHERE r.job_description_id=jd.id);

-- +goose Down
ALTER TABLE job_description_ability_requirement_options DROP COLUMN scope_details,DROP COLUMN normalization_relation;
ALTER TABLE abilities DROP COLUMN confused_with,DROP COLUMN exclude_signals,DROP COLUMN include_signals;
