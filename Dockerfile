FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o servregistry main.go

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/servregistry .
EXPOSE 8088
ENTRYPOINT ["./servregistry"]
