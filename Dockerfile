# syntax=docker/dockerfile:1
# A scratch image holding just /vmd1 (static, per architecture). Servers take it with `docker create` + `docker cp`.
FROM --platform=$BUILDPLATFORM golang:1.23-alpine@sha256:383395b794dffa5b53012a212365d40c8e37109a626ca30d6151c8348d380b5f AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /vmd1 ./cmd/vmd1

FROM scratch
COPY --from=build /vmd1 /vmd1
ENTRYPOINT ["/vmd1"]
