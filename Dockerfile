FROM debian:bookworm-slim

ARG TARGETARCH

ENV YCY_TUNNEL_DOCKER=1

COPY docker-assets/ycy-linux-x64 /tmp/ycy-linux-x64
COPY docker-assets/ycy-linux-arm64 /tmp/ycy-linux-arm64
COPY .tmp/docker/frp /tmp/frp

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends procps; \
    rm -rf /var/lib/apt/lists/*; \
    install -d /opt/ycy/frp/0.70.1 /usr/local/bin; \
    case "$TARGETARCH" in \
      amd64) cp /tmp/ycy-linux-x64 /usr/local/bin/ycy; cp /tmp/frp/linux-x64/frpc /opt/ycy/frp/0.70.1/frpc; cp /tmp/frp/linux-x64/frps /opt/ycy/frp/0.70.1/frps ;; \
      arm64) cp /tmp/ycy-linux-arm64 /usr/local/bin/ycy; cp /tmp/frp/linux-arm64/frpc /opt/ycy/frp/0.70.1/frpc; cp /tmp/frp/linux-arm64/frps /opt/ycy/frp/0.70.1/frps ;; \
      *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    chmod 0755 /usr/local/bin/ycy /opt/ycy/frp/0.70.1/frpc /opt/ycy/frp/0.70.1/frps; \
    rm -rf /tmp/ycy-linux-* /tmp/frp

ENTRYPOINT ["ycy"]
