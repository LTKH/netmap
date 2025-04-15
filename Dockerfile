ARG GOLANG_IMAGE="golang:1.21"
ARG ALPINE_IMAGE="alpine:3.21.3"

FROM ${GOLANG_IMAGE}

COPY . /src/
WORKDIR /src/

RUN go build -o /bin/netserver cmd/netserver/netserver.go

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

COPY --from=0 /bin/netserver /bin/netserver
COPY config/config.yml /etc/cdserver.yml

USER $USER_NAME

ENTRYPOINT ["/bin/netserver"]
CMD ["-config.file=/etc/netserver.yml"]
