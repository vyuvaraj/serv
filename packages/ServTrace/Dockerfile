FROM golang:alpine AS builder
WORKDIR /app
COPY . .
RUN sed -i 's/go 1\.2[5-9]\.[0-9]*/go 1.24/g' go.mod vendor/modules.txt 2>/dev/null; \
    CGO_ENABLED=0 go build -mod=vendor -o servtrace

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/servtrace .
EXPOSE 8090
ENTRYPOINT ["./servtrace"]
