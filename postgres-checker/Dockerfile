FROM alpine:3.21

RUN apk update && apk add bash curl ca-certificates postgresql-client && rm -rf /var/cache/apk/*
COPY checkdb.sh /checkdb.sh
RUN chmod +x /checkdb.sh

ENTRYPOINT ["/checkdb.sh"]
