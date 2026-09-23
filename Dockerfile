# ---------- 第一阶段：构建前端 ----------
FROM node:22-alpine AS web
WORKDIR /build/web
# 先只拷贝依赖清单，依赖没变时这层能命中缓存，改业务代码不必重装 node_modules。
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund || npm install --no-audit --no-fund
COPY web/ ./
# Vite 的 outDir 指向 ../internal/web/dist，需要这个目录存在。
RUN mkdir -p /build/internal/web && npm run build

# ---------- 第二阶段：构建后端 ----------
FROM golang:1.26-alpine AS server
WORKDIR /build
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
# 把前端产物放进 embed 目录，这样最终产物是单个可执行文件。
COPY --from=web /build/internal/web/dist ./internal/web/dist
ARG VERSION=dev
# CGO 关掉，用纯 Go 的 SQLite 驱动，产物可以直接跑在 alpine 上。
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/leyun ./cmd/leyun

# ---------- 第三阶段：运行时 ----------
FROM alpine:3.21
# tzdata 让容器里的时间戳是本地时区；ca-certificates 供 Office 回调走 https 时校验证书。
# su-exec 用来在入口脚本里降权，比 su/sudo 干净：不另起进程，信号能直达。
RUN apk add --no-cache ca-certificates tzdata wget su-exec && \
    adduser -D -u 1000 -h /app leyun
ENV TZ=Asia/Shanghai
WORKDIR /app
COPY --from=server /out/leyun /app/leyun
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
# data 目录由 compose 挂成卷；这里先建好并交给非 root 用户。
# 挂载会把镜像里这份属主盖掉，所以入口脚本启动时还会再校一次。
RUN mkdir -p /app/data && chmod +x /app/docker-entrypoint.sh && chown -R leyun:leyun /app
# 容器以 root 起，但入口脚本把属主理顺之后立刻 su-exec 成 leyun，
# 正式进程全程是非 root。不能直接写 USER leyun——那样就没权限修属主了，
# 而宿主机挂进来的 data 目录属主不对正是最常见的启动失败原因。
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/api/v1/health || exit 1
ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["-config", "/app/config.yaml"]
