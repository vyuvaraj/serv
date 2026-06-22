FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o servtunnel main.go

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/servtunnel .
EXPOSE 8443
ENTRYPOINT ["./servtunnel"]
CMD ["server"]
