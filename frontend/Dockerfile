# JobPilot 前端镜像。
#
# 前端所有请求都走同源相对路径（/api/v1/*、/auth/v1/*），没有构建期注入的 API 地址，
# 因此同一份产物可以用于任何域名或 IP，无需按环境重新构建。
#
# 运行层是 Caddy：既提供静态文件，也在 deploy/Caddyfile 里把 /api 和 /auth 反代到后端。
# Caddyfile 由 compose 挂载，不打进镜像，改完重启容器即可生效。
#
# npm 源默认为国内镜像；境外构建可用 --build-arg NPM_REGISTRY=https://registry.npmjs.org/ 覆盖。

FROM node:24-alpine AS build

ARG NPM_REGISTRY=https://registry.npmmirror.com/
RUN npm config set registry "${NPM_REGISTRY}"

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

COPY . .
RUN npm run build

FROM caddy:2-alpine
COPY --from=build /app/dist /srv
