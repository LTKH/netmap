FROM golang:1.19 AS builder

COPY . /src/
WORKDIR /src/
RUN go build -o /bin/netserver cmd/netserver/netserver.go

FROM alpine:latest

EXPOSE 8082

RUN addgroup -S netgroup && adduser -S netuser -G netgroup -u 1000 -s /bin/sh

WORKDIR /data
VOLUME ["/data"]

RUN chown -R netuser:netgroup /data && chmod 755 /data
USER netuser

COPY --from=builder /bin/netserver /bin/netserver
COPY config/config.yml /etc/netserver.yml

ENTRYPOINT ["/bin/sh","/bin/netserver"]
CMD ["-config.file=/etc/netserver.yml"]