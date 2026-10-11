# Builds hqd. The SQLite driver is pure Go, so the binary is static and the
# build cross-compiles for the target platform without emulation.
FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/hqd ./cmd/hqd
# Distroless has no shell to create this; a volume mounted here starts out
# with the image's ownership, so the nonroot user can write the database.
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/hqd /hqd
COPY --from=build --chown=nonroot:nonroot /out/data /data
EXPOSE 8080
ENTRYPOINT ["/hqd", "-addr", ":8080", "-db", "/data/hq.db"]
