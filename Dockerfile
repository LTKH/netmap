FROM golang:1.21

COPY . /src/
WORKDIR /src/
RUN go build -o /bin/netserver cmd/netserver/netserver.go

FROM redhat/ubi9-minimal

EXPOSE 8084

COPY --from=0 /bin/netserver /bin/netserver
COPY config/config.yml /etc/netserver.yml

VOLUME ["/data"]

USER nobody
#RUN /bin/netserver --help

ENTRYPOINT ["/bin/netserver"]
CMD ["-config.file=/etc/netserver.yml"]