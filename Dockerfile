##
## Build
##
FROM golang:1.19 AS build

WORKDIR /app

COPY go.mod ./
COPY go.sum ./

# Cache dependencies before building and copying the source code, so that
# we don't need to re-download when building and so that the change in the
# source code do not invalidate our downloaded layer.
RUN go mod download

COPY ./pkg ./pkg
COPY ./cmd ./cmd

# This runs a bit slower but guarantees that the binary does not rely on
# the underlying C environment (e.g. as "static" as possible).
RUN CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64 \
    go build -o /waterfall ./cmd/sfu

##
## Deploy
##
FROM scratch

WORKDIR /

COPY --from=build /waterfall /usr/bin/waterfall

# We need root certificates since we use HTTPS (TLS).
# https://uzimihsr.github.io/post/2022-09-29-golang-scratch-trust-cert/#trust-the-certificate-in-a-scratch-image
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

ENTRYPOINT ["waterfall"]
