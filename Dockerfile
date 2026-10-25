FROM node:24 AS webui-builder

WORKDIR /app/webui
COPY webui/package.json webui/package-lock.json ./
RUN npm ci
COPY config.example.json /app/config.example.json
COPY webui ./
RUN npm run build

FROM golang:1.26 AS go-builder
WORKDIR /app
ARG TARGETOS
ARG TARGETARCH
ARG BUILD_VERSION
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN set -eux; \
    GOOS="${TARGETOS:-$(go env GOOS)}"; \
    GOARCH="${TARGETARCH:-$(go env GOARCH)}"; \
    BUILD_VERSION_RESOLVED="${BUILD_VERSION:-}"; \
    if [ -z "${BUILD_VERSION_RESOLVED}" ] && [ -f VERSION ]; then BUILD_VERSION_RESOLVED="$(cat VERSION | tr -d "[:space:]")"; fi; \
    CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" go build -buildvcs=false -ldflags="-s -w -X ds2api/internal/version.BuildVersion=${BUILD_VERSION_RESOLVED}" -o /out/ds2api ./cmd/ds2api

FROM busybox:1.36.1-musl AS busybox-tools

# 下载与目标架构匹配的 Mihomo 内核（裸 ELF gzip 资产），
# 使镜像开箱即带代理桥能力，无需用户手动下载。
# 国内构建可传 --build-arg MIHOMO_MIRROR=https://ghfast.top/ 走加速前缀。
FROM debian:bookworm-slim AS mihomo-downloader
ARG MIHOMO_VERSION=v1.19.29
ARG MIHOMO_MIRROR=
ARG TARGETARCH
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends curl ca-certificates gzip; \
    rm -rf /var/lib/apt/lists/*; \
    case "${TARGETARCH:-amd64}" in \
      amd64|arm64) MIHOMO_ARCH="${TARGETARCH:-amd64}" ;; \
      *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    ASSET="mihomo-linux-${MIHOMO_ARCH}-${MIHOMO_VERSION}.gz"; \
    RELEASE_URL="https://github.com/MetaCubeX/mihomo/releases/download/${MIHOMO_VERSION}/${ASSET}"; \
    mkdir -p /out; \
    ok=0; \
    for url in "${RELEASE_URL}" "${MIHOMO_MIRROR}${RELEASE_URL}"; do \
      [ -z "${url}" ] && continue; \
      if curl -fL --retry 3 --connect-timeout 20 -o /tmp/mihomo.gz "${url}"; then ok=1; break; fi; \
    done; \
    [ "${ok}" = "1" ]; \
    gzip -dc /tmp/mihomo.gz > /out/mihomo; \
    rm -f /tmp/mihomo.gz; \
    chmod 0755 /out/mihomo

FROM debian:bookworm-slim AS runtime-base
WORKDIR /app
# 必须安装 CA 根证书：否则容器内拉取 HTTPS 机场订阅会报
# x509: certificate signed by unknown authority。
# update-ca-certificates 确保系统根证书库就绪（含内网/企业自建 CA 追加场景）。
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && update-ca-certificates \
    && groupadd -r ds2api && useradd -r -g ds2api -d /app -s /sbin/nologin ds2api \
    && mkdir -p /app/data /data && chown -R ds2api:ds2api /app /data
COPY --from=busybox-tools /bin/busybox /usr/local/bin/busybox
COPY --from=mihomo-downloader /out/mihomo /usr/local/bin/mihomo
EXPOSE 5001
CMD ["/usr/local/bin/ds2api"]

FROM runtime-base AS runtime-from-source
COPY --from=go-builder /out/ds2api /usr/local/bin/ds2api

COPY --from=go-builder --chown=ds2api:ds2api /app/config.example.json /app/config.example.json
COPY --from=webui-builder --chown=ds2api:ds2api /app/static/admin /app/static/admin
USER ds2api

FROM busybox-tools AS dist-extract
ARG TARGETARCH
COPY dist/docker-input/linux_amd64.tar.gz /tmp/ds2api_linux_amd64.tar.gz
COPY dist/docker-input/linux_arm64.tar.gz /tmp/ds2api_linux_arm64.tar.gz
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) ARCHIVE="/tmp/ds2api_linux_amd64.tar.gz" ;; \
      arm64) ARCHIVE="/tmp/ds2api_linux_arm64.tar.gz" ;; \
      *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    tar -xzf "${ARCHIVE}" -C /tmp; \
    PKG_DIR="$(find /tmp -maxdepth 1 -type d -name "ds2api_*_linux_${TARGETARCH}" | head -n1)"; \
    test -n "${PKG_DIR}"; \
    mkdir -p /out/static; \
    cp "${PKG_DIR}/ds2api" /out/ds2api; \
    cp "${PKG_DIR}/config.example.json" /out/config.example.json; \
    cp -R "${PKG_DIR}/static/admin" /out/static/admin

FROM runtime-base AS runtime-from-dist
COPY --from=dist-extract /out/ds2api /usr/local/bin/ds2api

COPY --from=dist-extract --chown=ds2api:ds2api /out/config.example.json /app/config.example.json
COPY --from=dist-extract --chown=ds2api:ds2api /out/static/admin /app/static/admin
USER ds2api

FROM runtime-from-source AS final
