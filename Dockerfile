# ---- Stage 1: Build frontend ----
FROM swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/node:20-alpine AS frontend-builder
WORKDIR /build
ENV NPM_CONFIG_REGISTRY=https://registry.npmmirror.com
COPY frontend/package*.json ./
RUN npm ci --prefer-offline --no-audit
COPY frontend/ .
RUN npm run build

# ---- Stage 2: Build Go backend ----
FROM swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/golang:1.25-alpine AS backend-builder
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories
RUN apk add --no-cache gcc musl-dev
WORKDIR /build
ENV GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
COPY pi-local/ ./pi-local/
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -a -installsuffix cgo -o PromAI .

# ---- Stage 3: Runtime ----
FROM swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/library/alpine:3.21
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
  && apk add --no-cache tzdata ca-certificates curl sqlite
# 默认使用中国时区，否则定时任务（cron）会按 UTC 触发，与北京时间相差 8 小时
ENV TZ=Asia/Shanghai
RUN ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime
WORKDIR /app
COPY --from=backend-builder /build/PromAI .
COPY --from=frontend-builder /build/dist ./frontend/dist/
COPY deploy/sql ./deploy/sql/
COPY templates ./templates/
# config.yaml 含敏感凭据且已被 .gitignore 忽略，镜像内使用示例模板；
# 真实凭据必须通过环境变量注入（PROMAI_AUTH_PASSWORD / PROMAI_JWT_SECRET / PROMETHEUS_PASSWORD 等）
COPY config/config.example.yaml ./config/config.yaml
COPY skills   ./skills/
RUN mkdir -p /app/data /app/reports
EXPOSE 8091
VOLUME ["/app/data", "/app/reports"]
ENTRYPOINT ["./PromAI"]
CMD ["-config", "/app/config/config.yaml"]
