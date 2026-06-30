FROM golang:alpine AS builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor -o servdb

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/servdb .
EXPOSE 8097
ENTRYPOINT ["./servdb"]
