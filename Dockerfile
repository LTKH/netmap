ARG GOLANG_IMAGE="golang:1.21"
ARG ALPINE_IMAGE="alpine"

FROM ${GOLANG_IMAGE} AS builder

COPY . /src/
WORKDIR /src/

RUN CGO_ENABLED=1 go build -o /bin/netserver cmd/netserver/netserver.go

FROM ${ALPINE_IMAGE}

EXPOSE 8084

ENV USER_ID=1000
ENV GROUP_ID=1000
ENV USER_NAME=netserver
ENV GROUP_NAME=netserver

RUN mkdir /data && chmod 755 /data && \
    addgroup -S -g $GROUP_ID $GROUP_NAME && \
    adduser -S -u $USER_ID -G $GROUP_NAME $USER_NAME && \
    chown -R $USER_NAME:$GROUP_NAME /data

COPY --from=builder /bin/netserver /bin/netserver
COPY config/config.yml /etc/netserver.yml

USER $USER_NAME

ENTRYPOINT ["/bin/netserver"]
CMD ["-config.file=/etc/netserver.yml"]