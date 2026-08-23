# syntax=docker/dockerfile:1

# --- derleme aşaması ---
FROM golang:1.26-alpine AS build

ARG VERSION=dev

WORKDIR /src

# Bağımlılıkları önce çek: kaynak değişse de bu katman cache'te kalır.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/gobit ./cmd/server

# --- çalışma aşaması ---
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/gobit /usr/local/bin/gobit

USER nonroot:nonroot
EXPOSE 9000

ENTRYPOINT ["/usr/local/bin/gobit"]
