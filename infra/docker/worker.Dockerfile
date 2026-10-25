# syntax=docker/dockerfile:1.7
FROM golang:1.26-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM alpine:3.20 AS runtime
RUN apk add --no-cache ca-certificates && \
    addgroup -S deeparchive && adduser -S deeparchive -G deeparchive
COPY --from=build /out/worker /usr/local/bin/worker
WORKDIR /app
USER deeparchive
ENTRYPOINT ["/usr/local/bin/worker"]
