FROM golang:1.17 AS builder

COPY . /src/
WORKDIR /src/
RUN go build -o /bin/netserver cmd/netserver/netserver.go

FROM alpine:3.15.0

ADD https://github.com/sgerrand/alpine-pkg-glibc/releases/download/2.34-r0/glibc-2.34-r0.apk /tmp
RUN apk update && \
    apk add --no-cache bash curl && \ 
    apk add --allow-untrusted /tmp/*.apk && rm -f /tmp/*.apk

EXPOSE 8082
WORKDIR /data
VOLUME ["/data"]

COPY --from=builder /bin/netserver /bin/netserver
COPY config/config.yml /etc/netserver.yml

ENTRYPOINT ["/bin/netserver"]
CMD ["-config=/etc/netserver.yml"]