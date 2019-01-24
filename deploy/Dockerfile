FROM golang:alpine as builder
MAINTAINER Jack Murdock <jack_murdock@comcast.com>

WORKDIR /go/src/github.com/comcast/codex-svalinn

RUN apk add --update git curl
RUN curl https://raw.githubusercontent.com/golang/dep/master/install.sh | sh

COPY . .
RUN dep ensure
COPY . .

RUN go build -o svalinn_linux_amd64 github.com/comcast/codex-svalinn

FROM alpine

RUN apk --no-cache add ca-certificates
RUN mkdir -p /etc/svalinn
VOLUME /etc/svalinn

EXPOSE 8080

COPY --from=builder /go/src/github.com/comcast/codex-svalinn/svalinn_linux_amd64 /
COPY conf/svalinn.yaml /
ENTRYPOINT ["/svalinn_linux_amd64"]
