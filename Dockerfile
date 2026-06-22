FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
RUN go build -o servcloud main.go

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/servcloud .
EXPOSE 8085
ENTRYPOINT ["./servcloud"]
