FROM scratch
MAINTAINER Misha Nasledov <misha@nasledov.com>
MAINTAINER Arnaud de Mouhy <arnaud.demouhy@akerbis.com>

COPY ./docker/rootfs/ /

EXPOSE 9898

CMD ["/usr/bin/uniqush-push"]
