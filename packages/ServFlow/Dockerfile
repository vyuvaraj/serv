FROM golang:alpine AS builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor -o servflow

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/servflow .
EXPOSE 8096
ENTRYPOINT ["./servflow"]
