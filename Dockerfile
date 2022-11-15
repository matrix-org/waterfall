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

COPY ./src ./src

RUN go build -o /waterfall ./src


##
## Deploy
##
FROM ubuntu:22.04

RUN apt update \
    && apt install -y --no-install-recommends \
    dumb-init \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /

COPY --from=build /waterfall /usr/bin/waterfall

ENTRYPOINT ["dumb-init", "--"]
CMD ["waterfall"]
