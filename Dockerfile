FROM --platform=$BUILDPLATFORM golang:1.27.2 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/adapter ./cmd/adapter
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/adapter /adapter
COPY --from=build --chown=65532:65532 /out/data /data
ENV LISTEN_ADDR=0.0.0.0:8080 DATA_DIR=/data GOMEMLIMIT=256MiB
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/adapter", "--healthcheck"]
ENTRYPOINT ["/adapter"]
