FROM golang:1.19.2 AS builder

COPY . /src/
WORKDIR /src/
RUN go build -o /bin/netserver cmd/netserver/netserver.go

#FROM ubuntu:latest AS scratchuser
#RUN useradd -u 10001 netserver

FROM scratch

EXPOSE 8084

#COPY --from=scratchuser /etc/passwd /etc/passwd
#RUN echo 'nobody:x:65534:65534:Nobody:/:' >> /etc/passwd
COPY --from=builder /bin/netserver /bin/netserver
COPY config/config.yml /etc/netserver.yml

VOLUME ["/data"]

USER nobody
#RUN /bin/netserver --help
#RUN cat /etc/passwd

ENTRYPOINT ["/bin/netserver"]
CMD ["-config.file=/etc/netserver.yml"]