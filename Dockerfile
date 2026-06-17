FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o servgate main.go

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/servgate .
COPY config.json .
EXPOSE 8080
ENTRYPOINT ["./servgate"]
