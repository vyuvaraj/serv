FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
RUN go build -o servcron main.go

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/servcron .
EXPOSE 8087
ENTRYPOINT ["./servcron"]
