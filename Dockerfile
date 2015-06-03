FROM centos:7
MAINTAINER Misha Nasledov <misha@nasledov.com>

RUN /usr/bin/yum -y install golang git gcc make mercurial-hgk wget

ENV GOBIN /tmp/bin
ENV GOPATH /tmp

RUN go get github.com/uniqush/uniqush-push

COPY conf/uniqush-push.conf .

RUN cp /tmp/bin/uniqush-push /usr/bin \
    && mkdir /etc/uniqush/ \
    && cp ./uniqush-push.conf /etc/uniqush/ \
    && sed -i -e 's/localhost/0.0.0.0/' /etc/uniqush/uniqush-push.conf

EXPOSE 9898

CMD ["/usr/bin/uniqush-push"]
