# A fleet node: a real `outfit daemon` supervising a fake engine.
#
# The engine is Imposter's native engine standing in for llama-server, so the
# stack needs no GPU and no model. It is a real process the daemon starts and
# supervises — see shim/llama-server for why that matters.

# Build outfit from the working tree, so the stack tests THIS commit rather
# than a published artifact.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# Only what the binary needs, so the image cannot depend on anything else in
# the tree and rebuilds stay cheap.
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -o /out/outfit ./cmd/outfit

FROM alpine:3.20
ARG TARGETARCH
# Pinned so a node image is reproducible; bump deliberately.
ARG IMPOSTER_VERSION=5.21.3

# procps gives the collector a vmstat whose output matches what it parses;
# BusyBox's differs, so CPU would otherwise be absent. Absent sources degrade
# gracefully by design — there is still no nvidia-smi here, so GPU stats are
# legitimately missing — but a dashboard with CPU is a better demo.
RUN apk add --no-cache ca-certificates curl bash procps

# Imposter's native engine — a standalone binary, which is what lets the daemon
# supervise it as a child process the way it would a real engine.
RUN curl -fsSL -o /tmp/imposter.tar.gz \
      "https://github.com/imposter-project/imposter-go/releases/download/v${IMPOSTER_VERSION}/imposter-go_linux_${TARGETARCH}.tar.gz" \
 && curl -fsSL -o /tmp/checksums.txt \
      "https://github.com/imposter-project/imposter-go/releases/download/v${IMPOSTER_VERSION}/checksums.txt" \
 && (cd /tmp && grep " imposter-go_linux_${TARGETARCH}.tar.gz\$" checksums.txt | sed 's/imposter-go_linux_.*/imposter.tar.gz/' | sha256sum -c -) \
 && tar -xzf /tmp/imposter.tar.gz -C /usr/local/bin imposter-go \
 && chmod +x /usr/local/bin/imposter-go \
 && rm -f /tmp/imposter.tar.gz /tmp/checksums.txt

COPY --from=build /out/outfit /usr/local/bin/outfit
COPY examples/fleet-docker/shim/llama-server /usr/local/bin/llama-server
COPY examples/fleet-docker/engine /opt/engine
RUN chmod +x /usr/local/bin/llama-server

# A container has no useful $HOME, so pin outfit's config directory — the same
# reason the cloud instance's daemon unit pins it.
ENV OUTFIT_CONFIG_DIR=/var/lib/outfit
WORKDIR /opt/node

EXPOSE 4242
# Bind the control API on all interfaces so other containers can reach it; a
# token is required for that, supplied per-node by compose. No Outfit and no
# arguments: the daemon runs what a start request tells it to.
CMD ["outfit", "daemon", "--api-addr", "0.0.0.0:4242"]
