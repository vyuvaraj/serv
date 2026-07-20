FROM golang:alpine AS builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor -o servauth

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/servauth .
EXPOSE 8098
ENTRYPOINT ["./servauth"]
