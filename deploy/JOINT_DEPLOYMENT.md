# JobPilot 与 StudyFlow 联合部署

核对日期：2026-10-17。本文维护两个网站的联合运行拓扑、配置和源码启动。固定镜像发布另见 [CI/CD](CICD.md)。

## 组成与数据

| 服务 | 用途 |
| --- | --- |
| web / Caddy | 两个网站的 HTTPS、认证与 API 入口 |
| api | JobPilot 页面、API、Agent 与 Worker |
| studyflow | StudyFlow 页面、HTTP、MCP 与 SSE |
| auth / auth-keygen | 账号、令牌与持久化签名密钥 |
| postgres | JobPilot 和认证两个逻辑库，启用 pgvector |
| mysql | StudyFlow 业务数据 |
| redis | 可丢失的 StudyFlow 聚合缓存 |

正常配置只由 web 发布 80/443，数据库与后端不映射公网。共享账号仍在两个网站分别登录。

源码构建要求三个仓库同级，或设置 `SHARED_AUTH_CONTEXT`、`STUDYFLOW_CONTEXT`：

~~~text
/srv/jobpilot-stack/
├─ jobpilot/deploy/
├─ shared-auth/
└─ studyflow/
~~~

## 配置与密钥

本目录执行：

~~~bash
test -f .env.joint || cp .env.joint.example .env.joint
bash generate-secrets.sh .env.joint
~~~

域名部署填写：

~~~dotenv
PUBLIC_ORIGIN=https://jobpilot.example.com
SITE_ADDRESS=jobpilot.example.com
STUDYFLOW_WEB_URL=https://studyflow.example.com
STUDYFLOW_SITE_ADDRESS=studyflow.example.com
STUDYFLOW_MCP_URL=https://studyflow.example.com/mcp
COOKIE_SECURE=true
~~~

将示例域名换成实际域名并核对 DNS、端口和证书。补齐真实邮件配置与所需平台模型配置；只启动容器不代表审核、判级或向量服务已就绪。

生成器保留已有值，新增独立 MySQL 密码；不要用新值覆盖接续数据使用的密钥。环境文件留在服务器，不提交仓库。

## 路线与认证

联合配置默认开启路线接入，同一 MCP 地址传给三个服务作为资源标识，同一交换密钥传给认证与 JobPilot。正常模式由 JobPilot 经 HTTPS 认证入口交换令牌，容器需能访问两个公开域名。

令牌绑定固定导入内容，不能作为 StudyFlow 普通登录。浏览器查看回执后仍正常登录、核对草稿并主动激活。配置细节见[学习路线](../docs/LEARNING_ROUTES.md)。

## 启动与维护

~~~bash
docker compose --env-file .env.joint -f compose.yaml -f compose.joint.yaml config --quiet
docker compose --env-file .env.joint -f compose.yaml -f compose.joint.yaml up -d --build --wait
docker compose --env-file .env.joint -f compose.yaml -f compose.joint.yaml ps
~~~

镜像部署在相同配置后叠加 `compose.images.yaml`，使用[发布说明](CICD.md)中的 digest 清单，不与源码发布命令混用。

源码更新或环境修改继续使用相同项目与文件组合。日志可通过同一 Compose 命令的 `logs --tail 100 api studyflow auth web` 查看。停止使用 `down` 并保留数据卷，不删除业务或签名密钥卷。

## HTTP 隔离联调

无域名的隔离测试可叠加 `compose.joint.http-test.yaml`，要求 Compose 2.24.4+。环境文件设置：

~~~dotenv
PUBLIC_ORIGIN=http://127.0.0.1:18080
STUDYFLOW_WEB_URL=http://127.0.0.1:18084
STUDYFLOW_MCP_URL=http://studyflow:8081/mcp
~~~

~~~bash
docker compose --env-file .env.joint -f compose.yaml -f compose.joint.yaml -f compose.joint.http-test.yaml config --quiet
docker compose --env-file .env.joint -f compose.yaml -f compose.joint.yaml -f compose.joint.http-test.yaml up -d --build --wait
~~~

覆盖配置默认绑定回环的 18080/18084，关闭 Cookie Secure，并显式允许固定服务间 HTTP 路线地址。通过 SSH 隧道从本机访问：

~~~bash
ssh -N -L 18080:127.0.0.1:18080 -L 18084:127.0.0.1:18084 user@server
~~~

这只改变指定服务地址的传输设置，不放宽账号、权限、摘要、幂等或禁止重定向校验。HTTP 联调不等于 HTTPS 正式入口验收；恢复正常配置时移除覆盖并恢复安全 Cookie 与域名地址。镜像发布脚本需与此组合一致，见[CI/CD](CICD.md)。

## 已有数据与验收

联合栈保留自身项目的 PostgreSQL 和签名密钥卷，新增 MySQL 卷；不会自动发现其他 Compose 项目、本机或旧版本的数据。迁移应分别备份导出业务库、认证库及密钥，再在隔离环境验证恢复。

验收至少包括两个网站、实际邮件、分别登录、资料隔离、模型任务、固定内容导入和手动激活；健康检查不覆盖这些结果。历史入口测试保留在[2026-10-14 记录](../docs/evaluations/deployment-20261014.md)，当前线上状态需新的运行证据。
