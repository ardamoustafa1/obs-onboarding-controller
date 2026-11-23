# Standalone image for the service-catalog REST API (see internal/catalog).
# Built and deployed independently of the operator manager image.
ARG BASE_IMAGE=golang:1.26
FROM ${BASE_IMAGE} AS builder
WORKDIR /workspace
COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -o /out/catalog ./cmd/catalog

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /out/catalog .
USER 65532:65532
ENTRYPOINT ["/catalog"]
