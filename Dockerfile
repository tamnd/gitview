# Consumed by GoReleaser: it copies the already cross-compiled binary out of
# the build context rather than compiling, so the image build is fast and uses
# the same static binary every other artifact ships. The base is Alpine (not
# distroless) because the local backend shells out to a real git binary.
#
# GoReleaser builds one multi-platform image with buildx and stages each
# platform's binary under a $TARGETPLATFORM directory (e.g. linux/amd64/) in
# the build context, so the COPY line selects the right one through the
# automatic TARGETPLATFORM build arg.
FROM alpine:3.21

ARG TARGETPLATFORM

RUN apk add --no-cache ca-certificates git tzdata \
 && adduser -D -H -u 10001 gitview \
 && mkdir -p /repos \
 && chown gitview:gitview /repos

COPY $TARGETPLATFORM/gitview /usr/bin/gitview

USER gitview
WORKDIR /repos

# Mount the repositories to browse at /repos:
#
#   docker run -v ~/src:/repos:ro -p 9419:9419 ghcr.io/tamnd/gitview
#
# The default bind address is loopback, which is unreachable from outside the
# container, so the entrypoint binds all interfaces; publish the port to taste.
VOLUME ["/repos"]
EXPOSE 9419

ENTRYPOINT ["/usr/bin/gitview", "-addr", "0.0.0.0:9419"]
CMD ["/repos"]
