# Build the manager binary, plus the gen-dashboard/gen-alerts CLIs this
# operator shells out to (see internal/gentools) -- baked into the same
# image rather than pulled from a separate registry so a single image
# digest fully determines rendering behavior.
#
# obs-golden-signal-templates and obs-alert-rule-templates are separate
# repos; this Dockerfile expects them as named BuildKit build contexts
# ("golden" and "alertrules"). Build with:
#
#   docker buildx build \
#     --build-context golden=../obs-golden-signal-templates \
#     --build-context alertrules=../obs-alert-rule-templates \
#     -t obs-onboarding-controller:dev .
#
# (obs-gitops-pipelines' CI checks out all three repos side by side before
# running this build -- see that repo's pipeline definition.)
ARG BASE_IMAGE=golang:1.26

FROM ${BASE_IMAGE} AS golden-builder
WORKDIR /src
COPY --from=golden . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/gen-dashboard ./cmd/gen-dashboard

FROM ${BASE_IMAGE} AS alerts-builder
WORKDIR /src
COPY --from=alertrules . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/gen-alerts ./cmd/gen-alerts

FROM ${BASE_IMAGE} AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /workspace

COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager cmd/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=golden-builder /out/gen-dashboard /usr/local/bin/gen-dashboard
COPY --from=alerts-builder /out/gen-alerts /usr/local/bin/gen-alerts
COPY --from=builder /workspace/manager .
USER 65532:65532

ENV GEN_DASHBOARD_BIN=/usr/local/bin/gen-dashboard
ENV GEN_ALERTS_BIN=/usr/local/bin/gen-alerts

ENTRYPOINT ["/manager"]
