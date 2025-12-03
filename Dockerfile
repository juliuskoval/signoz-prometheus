# =========================
# 1. Build stage
# =========================
FROM golang:1.25.3 AS builder

WORKDIR /app

# Download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o server .

# =========================
# 2. Runtime stage
# =========================
FROM alpine:latest

RUN adduser -D appuser

WORKDIR /app

COPY --from=builder /app/server .

EXPOSE 8081

USER appuser

ENTRYPOINT ["./server"]
