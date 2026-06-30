# --- Build frontend ---
FROM swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/node:20-alpine AS frontend
WORKDIR /app/web
RUN npm config set registry https://registry.npmmirror.com
COPY server/web-ui/package*.json ./
RUN npm ci
COPY server/web-ui/ ./
RUN npm run build

# --- Build Go binary ---
FROM swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/library/golang:1.23-alpine3.22 AS builder
ENV GOPROXY=https://mirrors.aliyun.com/goproxy/,direct
RUN sed -i 's|https://dl-cdn.alpinelinux.org/alpine|https://mirrors.aliyun.com/alpine|g' /etc/apk/repositories \
    && apk add --no-cache git
WORKDIR /app
COPY go.work ./
COPY pkg/ ./pkg/
COPY client/go.mod client/go.sum ./client/
COPY server/ ./server/
COPY --from=frontend /app/web/dist ./server/internal/web/dist/
RUN cd server && CGO_ENABLED=0 go build -ldflags="-s -w" -o /safelink-server ./cmd/safelink-server

# --- Runtime ---
FROM swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/alpine:3.20.3
RUN sed -i 's|https://dl-cdn.alpinelinux.org/alpine|https://mirrors.aliyun.com/alpine|g' /etc/apk/repositories \
    && apk add --no-cache iptables iproute2 ca-certificates
COPY --from=builder /safelink-server /usr/local/bin/safelink-server
COPY server/configs/server.yaml /etc/safelink/server.yaml

EXPOSE 1562/udp
EXPOSE 8080/tcp

ENTRYPOINT ["safelink-server"]
CMD ["--nat-iface", "eth0"]
