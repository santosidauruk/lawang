# syntax=docker/dockerfile:1

FROM golang:1.26.7-alpine3.23 AS build-base

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH


FROM build-base AS build-api

RUN CGO_ENABLED=0 \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH} \
    go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/lawang-api \
    ./cmd/api


FROM build-base AS build-fake-provider

RUN CGO_ENABLED=0 \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH} \
    go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/fake-provider \
    ./cmd/fake-provider


FROM alpine:3.23.5 AS runtime-base

RUN apk add --no-cache ca-certificates \
    && addgroup -S lawang \
    && adduser -S -G lawang lawang

USER lawang


FROM runtime-base AS api

COPY --from=build-api /out/lawang-api /usr/local/bin/lawang-api

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/lawang-api"]


FROM runtime-base AS fake-provider

COPY --from=build-fake-provider /out/fake-provider /usr/local/bin/fake-provider

EXPOSE 8081

ENTRYPOINT ["/usr/local/bin/fake-provider"]
