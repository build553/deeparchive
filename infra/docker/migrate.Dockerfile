# syntax=docker/dockerfile:1.7
# Runs database migrations as a one-shot container (`make migrate-up`).
FROM golang:1.26-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate

FROM alpine:3.20 AS runtime
RUN apk add --no-cache ca-certificates && \
    addgroup -S deeparchive && adduser -S deeparchive -G deeparchive
COPY --from=build /out/migrate /usr/local/bin/migrate
COPY backend/db/migrations /app/db/migrations
WORKDIR /app
USER deeparchive
ENTRYPOINT ["/usr/local/bin/migrate"]
