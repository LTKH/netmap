ARG GOLANG_IMAGE="golang:1.23.0"
ARG RUNNER_IMAGE="busybox:1.36.1"

FROM ${GOLANG_IMAGE} AS builder

COPY . /src/
WORKDIR /src/

RUN go build -o /bin/netserver cmd/netserver/netserver.go

FROM ${RUNNER_IMAGE}

EXPOSE 8084 8085

ENV USER_ID=1000
ENV GROUP_ID=1000
ENV USER_NAME=netserver
ENV GROUP_NAME=netserver

RUN mkdir /data && chmod 755 /data && \
    addgroup -S -g $GROUP_ID $GROUP_NAME && \
    adduser -S -u $USER_ID -G $GROUP_NAME $USER_NAME && \
    chown -R $USER_NAME:$GROUP_NAME /data

USER $USER_NAME

COPY --from=builder /bin/netserver /bin/netserver
COPY config/config.yml /etc/netserver.yml

VOLUME ["/data"]

ENTRYPOINT ["/bin/netserver"]
CMD ["-config.file=/etc/netserver.yml"]