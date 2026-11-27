# ---------- 前端构建 ----------
FROM node:22-alpine AS web
# 默认官方 npm 源；网络受限时用 --build-arg NPM_REGISTRY=https://registry.npmmirror.com 覆盖。
ARG NPM_REGISTRY=https://registry.npmjs.org
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm config set registry "$NPM_REGISTRY" \
    && npm ci --no-audit --no-fund
COPY frontend/ ./
# vite.config.ts 固定输出到 ../backend/web
RUN npm run build

# ---------- 后端构建 ----------
FROM golang:1.26-alpine AS api
# 默认使用可直连的 Go 模块镜像；需要时用 --build-arg GOPROXY=... 覆盖。
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# modernc.org/sqlite 为纯 Go 实现，可关闭 CGO 生成静态二进制。
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/apiunit ./cmd/server

# ---------- 运行镜像 ----------
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 apiunit \
    && adduser -S -u 10001 -G apiunit -h /app apiunit \
    && mkdir -p /app /data \
    && chown -R 10001:10001 /data /app
COPY --from=api /out/apiunit /app/apiunit
COPY --from=web /src/backend/web /app/web

ENV ADDR=0.0.0.0:8788 \
    DATA_DIR=/data \
    WEB_DIR=/app/web
USER 10001:10001
EXPOSE 8788
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8788/api/health || exit 1
ENTRYPOINT ["/app/apiunit"]
