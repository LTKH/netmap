FROM golang:1.20.3 AS builder

COPY . /src/
WORKDIR /src/
RUN go build -o /bin/netserver cmd/netserver/netserver.go

FROM alpine:latest

EXPOSE 8084

ENV USER_ID=1000
ENV USER_NAME=netserver

RUN mkdir /data && chmod 755 /data && \
    adduser -D -u $USER_ID -h /data $USER_NAME && \
    chown -R $USER_NAME /data

COPY --from=builder /bin/netserver /bin/netserver
COPY config/config.yml /etc/netserver.yml

VOLUME ["/data"]

USER $USER_NAME

ENTRYPOINT ["/bin/netserver"]
CMD ["-config.file=/etc/netserver.yml"]