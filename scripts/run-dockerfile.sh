#!/bin/bash -e

TAG=$( git describe --always )
NAME=uniqush-push-$TAG

function finish {
    docker rm -f $NAME $NAME-redis >/dev/null 2>&1 || true
    docker network rm $NAME >/dev/null 2>&1 || true
}

trap finish EXIT

docker build --tag uniqush-build:$TAG .
docker network create $NAME
docker run --detach --name=$NAME-redis --network=$NAME --network-alias=redis redis:7
docker run \
       --name=$NAME \
       --network=$NAME \
       --publish=9898:9898 \
       uniqush-build:$TAG
