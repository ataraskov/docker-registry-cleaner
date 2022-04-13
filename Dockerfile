FROM golang:1.17.8-bullseye AS builder
ARG VERSION=dev \
    COMMIT=unknown
WORKDIR /go/src/app
COPY . .
RUN CGO_ENABLED=0 go build -o main -ldflags="-extldflags '-static' -X=main.version=${VERSION} -X=main.gitCommit=${COMMIT}" main.go

FROM scratch
COPY --from=builder /go/src/app/main /docker-registry-cleaner
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENTRYPOINT ["/docker-registry-cleaner"]

