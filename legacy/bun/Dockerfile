ARG TARGETARCH

FROM scratch AS ycy-amd64
COPY docker-artifacts/ycy-linux-x64/ycy-linux-x64 /ycy

FROM scratch AS ycy-arm64
COPY docker-artifacts/ycy-linux-arm64/ycy-linux-arm64 /ycy

FROM ycy-${TARGETARCH} AS ycy

FROM oven/bun:1.3.14 AS frp

WORKDIR /src

COPY package.json bun.lock ./
RUN bun install --frozen-lockfile --ignore-scripts

COPY scripts/install-tunnel-frp.ts scripts/install-tunnel-frp.ts
COPY src src

ARG TARGETARCH
RUN case "$TARGETARCH" in \
      amd64) BUN_ARCH=x64 ;; \
      arm64) BUN_ARCH=arm64 ;; \
      *) echo "Unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac \
    && XDG_STATE_HOME=/out/tunnel-state bun scripts/install-tunnel-frp.ts "$BUN_ARCH"

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /usr/share/licenses/frp \
    && cp /usr/share/common-licenses/Apache-2.0 /usr/share/licenses/frp/LICENSE

COPY --chmod=755 --from=ycy /ycy /usr/local/bin/ycy
COPY --chmod=755 --from=frp /out/tunnel-state/ycy/frp /opt/ycy/frp

ENV YCY_TUNNEL_DOCKER=1

ENTRYPOINT ["/usr/local/bin/ycy"]
