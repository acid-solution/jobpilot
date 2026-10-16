# CI/CD

GitHub Actions 在 push、PR 和手动触发时执行检查。只有受信任的 main 构建会发布镜像；PR 不获取 SSH 凭证，也不发布镜像或连接服务器。第三方 Action 固定到提交 SHA。

## CI

- JobPilot：临时 PostgreSQL 17＋pgvector；先运行 `TestMigrationsSmoke` 建好普通集成测试库，再运行 `go test -race -count=1 -v ./...` 和 `go vet ./...`。`JOBPILOT_TEST_DATABASE_URL`、`TEST_DATABASE_URL` 指向该测试库；`JOBPILOT_ADMIN_DATABASE_URL` 指向同一临时实例的 postgres 库，供隔离测试建库。真实模型测试默认关闭。
- 前端执行现有的类型检查和构建；尚未提供前端单元测试命令。
- 部署脚本测试在独立、无网络的容器中验证配置生成、单服务发布、发布失败、备份失败及两个仓库同时发布的锁。Docker CLI 使用测试替代实现；不能把这些结果当成真实服务器发布验收。
- 构建 linux/amd64 镜像。main 成功后发布 `ghcr.io/acid-solution/jobpilot:<commit>`，运行摘要记录不可变 digest。
- shared-auth 固定源码提交 `246e0a27fb90401dd58817339b4df4d868d31b2b`，构建为 `ghcr.io/acid-solution/jobpilot-auth`。首次部署固定其 digest；日常应用发布不更新认证服务。

## GitHub 配置

两个应用仓库各配置：

| 类型 | 名称 | 内容 |
| --- | --- | --- |
| Secret | `DEPLOY_SSH_KEY` | 专用部署私钥 |
| Secret | `DEPLOY_KNOWN_HOSTS` | 已从服务器 root 终端核对的主机公钥记录 |
| Variable | `DEPLOY_HOST` | 部署服务器地址 |
| Variable | `DEPLOY_PORT` | SSH 端口 |
| Variable | `DEPLOY_USER` | 专用部署账号，例如 `acidsolution-deploy` |
| Variable | `DEPLOY_PATH` | `/srv/jobpilot-stack/jobpilot/deploy` |
| Variable | `DEPLOY_ENABLED` | 初始为 `false`；联合栈首次验收通过后改为 `true` |

部署 job 使用 `production` environment；可在 GitHub 设置允许发布的分支和人工审核。部署关闭时仍执行 CI 和镜像发布，不会连接服务器。

另外提供手动触发的 `Verify SSH` 工作流：它只在 main 使用专用凭证核验服务器身份并读取登录用户名，不启动程序或修改服务器，可在 Docker 和网站准备完成前验证 GitHub 到服务器的实际连通性。

GHCR 新包默认可能是私有的。上线前确认服务器能拉取三个固定 digest：公开镜像可匿名拉取；保留私有时，在服务器配置专用、仅有 `read:packages` 的凭证，不使用个人的全权限 Token。不要把密码或 Token 放进仓库。镜像能否下载必须在目标服务器实测。

## 服务器准备与首次部署

1. 使用 root 先运行 `bash prepare-cd-server.sh --check`，检查计划；再运行 `--apply`。脚本通过 Docker 官方 Ubuntu APT 仓库安装 Docker/Compose，创建 `/srv/jobpilot-stack`，把部署账号加入 docker 组。不启动网站，不修改防火墙，不覆盖现有 Docker daemon 配置。**docker 组具有接近 root 的主机控制能力**；脚本不添加无限制 sudo。
2. 重新连接部署账号，核验 `docker info` 与 `docker compose version`。将本仓库放到 `/srv/jobpilot-stack/jobpilot`。镜像部署无需另外克隆应用源码到 StudyFlow 目录。
3. 按 `JOINT_DEPLOYMENT.md` 配置服务器专用 `.env.joint`，运行 `generate-secrets.sh .env.joint`，补齐邮件、模型、域名等实际配置。保留已有密钥和数据。
4. 复制 `.env.images.example` 为 `.env.images`，填入两个应用及认证的成功构建摘要中的完整 digest，不能填 latest 或占位符。
5. 在基础、联合配置后叠加 `compose.images.yaml`。生产例子：

   ```bash
   docker compose --env-file .env.joint --env-file .env.images \
     -f compose.yaml -f compose.joint.yaml -f compose.images.yaml \
     up -d --no-build --wait --wait-timeout 240
   ```

6. 无域名时先按联合部署文档进行 SSH 隧道联调：在 images 配置之前加 `compose.joint.http-test.yaml`，并创建空文件 `.cd-http-test` 让发布脚本使用相同组合。该配置默认只监听回环地址；公开 IP HTTP、TLS、备案、真实邮件及模型凭证必须另行验收，不能因 CI 通过就默认可以公网使用。
7. 验证两网站、登录、关键业务与授权后的学习路线导入，再启用两个仓库的 `DEPLOY_ENABLED=true`。必须先完成一次联合栈初始化；自动发布不会替你猜测首次配置或初始化数据库。

## 自动更新和恢复

`release.sh` 只接受本项目的 `image@sha256:<64位摘要>`，两个仓库通过同一个服务器文件锁串行发布。它拉取镜像、备份对应业务库、仅更新目标应用容器，再检查健康接口和首页。通过后才原子写入 `.env.images` 和发布历史。认证、数据库及 Caddy 不随单个应用发布重建。

备份和失败日志保存在服务器 `deploy/.release-state/`，权限为私有，不上传到 GitHub；应另行制定保留周期和异机备份策略。发布失败时保留原镜像清单、数据库备份和失败日志，流水线标红。失败后的实际容器可能已被替换，旧清单不等于旧程序已恢复。

数据库迁移随应用启动执行，脚本不自动降级数据库或假定旧程序一定兼容。确认数据库结构与旧版本兼容后，可用历史成功摘要重新执行 `bash release.sh jobpilot <previous-image-digest>`；不兼容时先制定数据恢复方案。健康和首页检查不等于完整业务端到端验收，正式发布还需相应业务测试。
