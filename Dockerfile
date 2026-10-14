FROM node:24-alpine AS frontend
ARG NPM_REGISTRY=https://registry.npmmirror.com/
RUN npm config set registry "${NPM_REGISTRY}"
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.26.4-alpine3.23 AS backend
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY} CGO_ENABLED=0 GOOS=linux
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN go build -trimpath -ldflags="-s -w" -o /out/jobpilot-server ./cmd/server

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=backend /out/jobpilot-server ./jobpilot-server
COPY --from=frontend /src/frontend/dist ./frontend/dist
RUN mkdir -p /app/config && chown -R app:app /app
ENV FRONTEND_DIST_DIR=/app/frontend/dist
USER app
EXPOSE 18081
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -qO- http://127.0.0.1:18081/health/ready >/dev/null || exit 1
ENTRYPOINT ["/app/jobpilot-server"]
