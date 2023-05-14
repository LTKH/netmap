FROM golang:1.20.3 AS builder

COPY . /src/
WORKDIR /src/
RUN go build -o /bin/netserver cmd/netserver/netserver.go

FROM redhat/ubi8-minimal

EXPOSE 8084

ENV USER_ID=1000
ENV GROUP_ID=1000
ENV USER_NAME=netserver
ENV GROUP_NAME=netserver

RUN mkdir /data && chmod 755 /data && \
    groupadd --gid $GROUP_ID $GROUP_NAME && \
    useradd -M --uid $USER_ID --gid $GROUP_ID --home /data $USER_NAME && \
    chown -R $USER_NAME:$GROUP_NAME /data

COPY --from=builder /bin/netserver /bin/netserver
COPY config/config.yml /etc/netserver.yml

VOLUME ["/data"]

USER $USER_NAME

ENTRYPOINT ["/bin/netserver"]
CMD ["-config.file=/etc/netserver.yml"]