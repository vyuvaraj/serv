FROM golang:alpine AS builder
WORKDIR /app
COPY . .
RUN sed -i 's/go 1\.2[5-9]\.[0-9]*/go 1.24/g' go.mod vendor/modules.txt && \
    CGO_ENABLED=0 go build -mod=vendor -o servmesh

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/servmesh .
EXPOSE 8089
ENTRYPOINT ["./servmesh"]
