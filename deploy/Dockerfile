FROM golang:1.25.6-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o secret2ssl main.go

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /app/secret2ssl .
COPY config/config.yaml ./config/config.yaml

EXPOSE 8080

CMD ["./secret2ssl"]