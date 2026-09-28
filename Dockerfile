# Expects the Go binary to already be built for linux/amd64 on the host
# (see the build script) — cross-compiling natively on the host avoids
# QEMU emulation crashes that occur running `go build` inside an emulated
# linux/amd64 container on an arm64 (Apple Silicon) machine.
FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    addgroup -g 10001 driftgcp && \
    adduser -D -u 10001 -G driftgcp driftgcp

COPY dist/driftgcp-linux-amd64 /usr/local/bin/driftgcp
COPY testdata/ /home/driftgcp/testdata/

RUN chown -R driftgcp:driftgcp /home/driftgcp
USER 10001:10001
WORKDIR /home/driftgcp

EXPOSE 8080
ENTRYPOINT ["driftgcp", "serve", "--addr", ":8080"]
