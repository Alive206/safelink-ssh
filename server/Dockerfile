# --- Build frontend ---
FROM node:20-alpine AS frontend
WORKDIR /app/web
COPY server/web/package*.json ./
RUN npm ci --silent 2>/dev/null || true
COPY server/web/ ./
RUN npm run build 2>/dev/null || mkdir -p dist && echo '<html><body>SafeLink Server</body></html>' > dist/index.html

# --- Build Go binary ---
FROM golang:1.23-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app
COPY go.work ./
COPY pkg/ ./pkg/
COPY server/ ./server/
COPY --from=frontend /app/web/dist ./server/internal/web/dist/
RUN cd server && CGO_ENABLED=0 go build -ldflags="-s -w" -o /safelink-server ./cmd/safelink-server

# --- Runtime ---
FROM alpine:3.20
RUN apk add --no-cache iptables iproute2 ca-certificates
COPY --from=builder /safelink-server /usr/local/bin/safelink-server
COPY server/configs/server.yaml /etc/safelink/server.yaml

EXPOSE 1562/udp
EXPOSE 8080/tcp

ENTRYPOINT ["safelink-server"]
CMD ["--nat-iface", "eth0"]
