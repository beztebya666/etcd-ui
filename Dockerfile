# syntax=docker/dockerfile:1.7

############################
# 1. Build frontend
############################
FROM node:20-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm install --no-audit --no-fund --legacy-peer-deps
COPY web/ ./
RUN npm run build

############################
# 2. Build Go binaries
############################
FROM golang:1.25-alpine AS gobuild
ARG BUILD_SHA=dev
ARG TARGETOS
ARG TARGETARCH
RUN apk add --no-cache ca-certificates git
WORKDIR /src
COPY . .
ENV CGO_ENABLED=0 GOFLAGS="-trimpath -mod=mod"
RUN go mod tidy
RUN mkdir -p /out && \
    for svc in supervisor gateway cluster kv ops audit; do \
        GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
        go build -ldflags="-s -w -X main.Version=${BUILD_SHA}" -o /out/$svc ./cmd/$svc ; \
    done

############################
# 3. Pull real etcdctl + etcdutl from the upstream etcd image
############################
FROM quay.io/coreos/etcd:v3.5.15 AS etcdctl
# /usr/local/bin/{etcdctl,etcdutl} ship in this image, statically linked.
# etcdctl is the online client; etcdutl is its offline counterpart used
# for snapshot inspection/restore against a stopped data dir.

############################
# 4. Final image
############################
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S etcdui && adduser -S -G etcdui etcdui && \
    mkdir -p /app/web /app/bin /app/data && \
    chown -R etcdui:etcdui /app

COPY --from=gobuild --chown=etcdui:etcdui /out/ /app/bin/
COPY --from=web --chown=etcdui:etcdui /web/dist /app/web
COPY --from=etcdctl /usr/local/bin/etcdctl /usr/local/bin/etcdctl
COPY --from=etcdctl /usr/local/bin/etcdutl /usr/local/bin/etcdutl

ENV ETCD_UI_WEB_ROOT=/app/web \
    ETCD_UI_GATEWAY_ADDR=:8080 \
    ETCD_UI_CLUSTER_ADDR=127.0.0.1:7001 \
    ETCD_UI_KV_ADDR=127.0.0.1:7002 \
    ETCD_UI_OPS_ADDR=127.0.0.1:7003 \
    ETCDCTL_API=3

USER etcdui
EXPOSE 8080
WORKDIR /app

HEALTHCHECK --interval=20s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/app/bin/supervisor"]
