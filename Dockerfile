FROM golang:1.20.3 AS builder

COPY . /src/
WORKDIR /src/
RUN go build -o /bin/netserver cmd/netserver/netserver.go

FROM redhat/ubi8-minimal

EXPOSE 8084

#ENV USER_ID=1000
#ENV GROUP_ID=1000
#ENV USER_NAME=netserver
#ENV GROUP_NAME=netserver

#ADD https://github.com/sgerrand/alpine-pkg-glibc/releases/download/2.34-r0/glibc-2.34-r0.apk /tmp
#RUN apk update && \
#    apk add --no-cache bash curl && \
#    apk add --allow-untrusted /tmp/*.apk && rm -f /tmp/*.apk

#RUN apk update && apk add --no-cache bash curl

#RUN addgroup -g $GROUP_ID $GROUP_NAME && \
#    adduser --shell /sbin/nologin --disabled-password --no-create-home --uid $USER_ID --ingroup $GROUP_NAME $USER_NAME
#
#RUN mkdir /data && chown -R $USER_NAME:$GROUP_NAME /data && chmod 755 /data

RUN mkdir /var/lib/sqlite3
VOLUME ["/var/lib/sqlite3"]

#WORKDIR /etc/netserver
#USER $USER_NAME

COPY --from=builder /bin/netserver /bin/netserver
COPY config/config.yml /etc/netserver.yml

ENTRYPOINT ["/bin/netserver"]
CMD ["-config.file=/etc/netserver.yml"]