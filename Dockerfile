# The build stage runs on the builder's own platform and cross-compiles, so a
# multi-platform build compiles natively rather than under emulation.
FROM --platform=$BUILDPLATFORM golang:1.25 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/uniqush-push .

# The stock config listens on localhost, logs to a file under /var/log and
# expects redis on localhost; none of that works inside a container. Listen on
# all interfaces, log to stderr, and look for redis at the host "redis". Mount
# your own config over /etc/uniqush/uniqush-push.conf to change any of it.
RUN mkdir -p /out/etc/uniqush \
    && sed -e 's/^addr=localhost:/addr=0.0.0.0:/' \
           -e 's/^logfile=.*/logfile=/' \
           -e 's/^\[Database\]$/[Database]\nhost=redis/' \
           conf/uniqush-push.conf > /out/etc/uniqush/uniqush-push.conf

FROM scratch

# uniqush verifies Apple, Google, Amazon and Web Push servers against the
# system roots, and refuses to start without them.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/etc/uniqush/uniqush-push.conf /etc/uniqush/uniqush-push.conf
COPY --from=build /out/uniqush-push /usr/bin/uniqush-push

USER 65534:65534
EXPOSE 9898

ENTRYPOINT ["/usr/bin/uniqush-push"]
