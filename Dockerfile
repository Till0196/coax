# Built by GoReleaser, which places each platform's binaries under
# $TARGETPLATFORM in the build context.
FROM alpine:3.24

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/coaxd $TARGETPLATFORM/coax $TARGETPLATFORM/fwextract /usr/bin/

# Running as root puts the sockets in /run/coaxd; mount it to reach the daemon
# from outside the container.
VOLUME /run/coaxd

ENTRYPOINT ["coaxd"]
CMD ["--idle", "0"]
