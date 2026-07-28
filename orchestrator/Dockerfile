# syntax=docker/dockerfile:1.7

# Keep source compatibility tested on Go 1.20, but build the network-facing
# image with a supported toolchain so current standard-library fixes are
# included in the static binary.
FROM --platform=$BUILDPLATFORM golang:1.26.5-bookworm AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src

# Download dependencies before copying sources to keep this layer reusable.
COPY go.mod go.sum go.work ./
COPY integrations/sage/go.mod integrations/sage/go.sum ./integrations/sage/
RUN go mod download \
    && cd integrations/sage \
    && go mod download

COPY pkg ./pkg
COPY integrations/sage ./integrations/sage
COPY config ./config

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build \
    -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /out/sage-nodescheduler \
    ./integrations/sage/cmd/waggle-nodescheduler

FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="SAGE resilient urgent scheduler" \
      org.opencontainers.image.description="Drop-in Waggle node scheduler with a resilient and urgency-aware policy"

COPY --from=builder /out/sage-nodescheduler /usr/local/bin/sage-nodescheduler
COPY --from=builder /src/config/policy.example.yaml /etc/sage-resilient-urgent-scheduler/policy.yaml
COPY --from=builder /src/integrations/sage/config/nodescheduler.example.yaml /etc/sage-resilient-urgent-scheduler/nodescheduler.yaml
COPY LICENSE NOTICE /licenses/sage-resilient-urgent-scheduler/

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/sage-nodescheduler"]
CMD ["--config", "/etc/sage-resilient-urgent-scheduler/nodescheduler.yaml"]
