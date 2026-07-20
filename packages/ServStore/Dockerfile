FROM golang:alpine AS builder
WORKDIR /src
COPY . .
RUN sed -i 's/go 1\.2[5-9]\.[0-9]*/go 1.24/g' go.mod vendor/modules.txt && \
    CGO_ENABLED=0 go build -mod=vendor -ldflags="-s -w" -o /servstore ./cmd/servstore

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /servstore /usr/local/bin/servstore

EXPOSE 8080 8090
VOLUME /data

ENTRYPOINT ["servstore"]
CMD ["--data-dir", "/data"]
