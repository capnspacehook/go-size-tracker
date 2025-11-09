FROM golang:1.25.4-alpine

COPY . /build
WORKDIR /build

# add git so VCS info will be stamped in binary
# ignore warning that a specific version of git isn't pinned
#hadolint ignore=DL3018
RUN apk add --no-cache git

ARG CGO_ENABLED=0
ARG VERSION=devel
RUN go build -buildvcs=true -ldflags "-s -w -X main.version=${VERSION}" -trimpath -o /bin/go-size-tracker

WORKDIR /work

RUN rm -rf /build

ENTRYPOINT [ "/bin/go-size-tracker" ]
