FROM golang:latest AS builder

COPY . /src/
WORKDIR /src/
RUN go build -o /bin/netserver cmd/netserver/netserver.go

FROM alpine:3.18.0

EXPOSE 8082

RUN addgroup -S netgroup && adduser -S netserver -G netgroup -u 1000 -s /bin/sh

WORKDIR /data
VOLUME ["/data"]

RUN chown -R netserver:netgroup /data && chmod 755 /data
USER netserver

COPY --from=builder /bin/netserver /bin/netserver
COPY config/config.yml /etc/netserver.yml

ENTRYPOINT ["/bin/netserver"]
CMD ["-config.file=/etc/netserver.yml"]