FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor -o servauth main.go

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/servauth .
EXPOSE 8098
ENTRYPOINT ["./servauth"]
