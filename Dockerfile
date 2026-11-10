# syntax=docker/dockerfile:1
# A scratch image holding just /vmd1 (static, per architecture). Servers take it with `docker create` + `docker cp`.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine@sha256:30b1e546a9c7fa3b747c2bdba3a0c7bdf3a02ebb4c5b8405d2f948d7e8aac4ec AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /vmd1 ./cmd/vmd1

FROM scratch
COPY --from=build /vmd1 /vmd1
ENTRYPOINT ["/vmd1"]
