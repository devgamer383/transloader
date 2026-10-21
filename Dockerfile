# Build Stage
FROM golang:1.23-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/bin/transloader cmd/transloader/main.go

# Production Stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata curl

WORKDIR /app
COPY --from=builder /app/bin/transloader /app/bin/transloader

ENV PORT=8080
ENV DOWNLOAD_DIR=/app/downloads
EXPOSE 8080

CMD ["/app/bin/transloader"]
