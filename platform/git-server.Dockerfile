FROM alpine:3.22
RUN apk add --no-cache git git-daemon
ENTRYPOINT ["git"]
