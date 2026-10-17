# CI/CD、发布与恢复

核对日期：2026-10-17。流程以[ci.yml](../.github/workflows/ci.yml)、[verify-ssh.yml](../.github/workflows/verify-ssh.yml)和 [release.sh](release.sh)为准。

## 检查与镜像

push、PR、手动触发均执行 CI，第三方 Action 固定提交。JobPilot 流水线包括：

1. 临时 PostgreSQL 17 + pgvector，先运行迁移 smoke，再运行 Go race、数据库集成和 vet。
2. 前端依赖安装、普通测试、安装 Chromium、显式开启无头假接口验收、类型检查与构建。
3. 无网络容器中的部署脚本替身测试，检查生成、备份失败、发布失败与服务器锁。
4. 构建 linux/amd64 镜像。只有受信任 main 发布到 GHCR，PR 不读取 SSH 凭据、不发布或连接服务器。

认证依赖固定源码提交 `246e0a27fb90401dd58817339b4df4d868d31b2b` 并发布为 `jobpilot-auth`。首次部署固定 digest，日常单应用发布不替换认证。

工作流摘要记录不可变镜像引用，不使用 latest 作为发布依据。

## GitHub 与服务器配置

两个应用仓库各配置：

| 类型 | 名称 | 用途 |
| --- | --- | --- |
| Secret | `DEPLOY_SSH_KEY` | 专用部署私钥 |
| Secret | `DEPLOY_KNOWN_HOSTS` | 已独立核对的主机公钥 |
| Variable | `DEPLOY_HOST`、`DEPLOY_PORT` | 目标主机与 SSH 端口 |
| Variable | `DEPLOY_USER` | 专用部署账号 |
| Variable | `DEPLOY_PATH` | 如 `/srv/jobpilot-stack/jobpilot/deploy` |
| Variable | `DEPLOY_ENABLED` | 明确为 true 才开启自动发布 |

部署使用 `production` environment。首次启用前应完成初始化与业务验收；当前服务器是否启用以其配置为准，不能由示例默认值推断。

手动 Verify SSH 工作流只核验连接与用户名，不部署。GHCR 镜像权限需使服务器能够拉取；私有包使用限权服务器凭据，不写入仓库。

## 首次准备

Ubuntu 准备脚本以 root 先运行检查，再显式应用：

~~~bash
bash prepare-cd-server.sh --check
bash prepare-cd-server.sh --apply
~~~

它要求既有专用部署账号，安装缺少的 Docker/Compose、准备目录并加入 docker 组；保留既有 daemon 配置，不启动网站或改防火墙。docker 组具有广泛主机控制权限。可选镜像参数、版本与签名约束以脚本为准。

按[联合部署](JOINT_DEPLOYMENT.md)准备 `.env.joint`。镜像方式不需要另克隆应用源码到两个构建目录：

~~~bash
test -f .env.images || cp .env.images.example .env.images
~~~

填入两个应用及认证各自成功 CI 的完整 `image@sha256:<64位摘要>`，然后：

~~~bash
docker compose --env-file .env.joint --env-file .env.images -f compose.yaml -f compose.joint.yaml -f compose.images.yaml config --quiet
docker compose --env-file .env.joint --env-file .env.images -f compose.yaml -f compose.joint.yaml -f compose.images.yaml up -d --no-build --wait --wait-timeout 240
~~~

HTTP 隔离联调在 images 前叠加 `compose.joint.http-test.yaml`，并在本目录创建空文件 `.cd-http-test`，保证后续脚本使用同一组合。正式域名模式不使用该标记。

## 单应用发布

~~~bash
bash release.sh jobpilot 'ghcr.io/acid-solution/jobpilot@sha256:<实际64位摘要>'
bash release.sh studyflow 'ghcr.io/acid-solution/studyflow@sha256:<实际64位摘要>'
~~~

以上占位摘要需替换，脚本只接受对应仓库的不可变引用。两个项目使用同一个服务器文件锁串行执行：

1. 核对环境、镜像清单和已初始化服务。
2. 拉取指定镜像。
3. 备份对应业务库至私有 `.release-state/backups`。
4. 仅替换指定应用，不重建认证、数据库或 Caddy。
5. 检查容器、数据库健康与首页。
6. 成功后原子更新 `.env.images` 并记录历史。

备份失败停止发布；失败时保存日志、旧镜像清单和备份，不报告成功。保留旧清单不说明实际旧容器已经恢复。

## 备份与回退

发布前备份只覆盖本次目标业务库，不自动覆盖全部认证库、JWT 私钥和资料加密密钥，也不等于周期异机备份。为这些对象分别安排保留与恢复演练。

数据库迁移随新应用启动，失败后实际数据库可能已改变。先核对迁移与旧应用兼容性，再决定重新发布旧成功 digest；脚本不自动降级数据库。

备份恢复应先在新建隔离库验证，再明确切换窗口和数据取舍。Down 迁移不是数据恢复，不能用删卷重建代替保留业务数据。

## 验证范围

无网络脚本测试只使用 Docker 替身，不证明 SSH、证书、邮件、镜像下载或模型可用。CI、部署健康、首页和业务端到端分别记录，入口见[验证指南](../docs/VALIDATION.md)。
