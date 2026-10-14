# JobPilot 后端镜像。
#
# 只编译 cmd/server。迁移文件通过 go:embed 打进二进制，运行层不需要额外拷贝
# migrations 目录，也不要挂载源码。
#
# 构建期的 Go 模块代理默认为国内镜像；阿里云 ECS 直连 proxy.golang.org 通常不可用。
# 需要在境外构建时用 --build-arg GOPROXY=https://proxy.golang.org,direct 覆盖。

FROM golang:1.26.4-alpine3.23 AS build

ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY} \
    CGO_ENABLED=0 \
    GOOS=linux

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/jobpilot-server ./cmd/server

FROM alpine:3.23

# ca-certificates 是必需的：DeepSeek、百炼 Embedding、GitHub API 全部走 HTTPS 出站。
# tzdata 用于日志和按日期统计按本地时区显示。
RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S app && adduser -S -G app app

WORKDIR /app
COPY --from=build /out/jobpilot-server /app/jobpilot-server

# 模型单价文件是可选输入，需要时通过 compose 挂载到 /app/config 并设置 MODEL_PRICING_FILE。
RUN mkdir -p /app/config && chown -R app:app /app

USER app
EXPOSE 18081

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -qO- http://127.0.0.1:18081/health/ready >/dev/null || exit 1

ENTRYPOINT ["/app/jobpilot-server"]
