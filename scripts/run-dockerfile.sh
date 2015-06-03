#!/bin/bash -e

TAG=$( git describe --always )

function finish {
    docker rm uniqush-push-$TAG
}

trap finish EXIT

docker build --tag uniqush-build:$TAG .
docker run \
       --name=uniqush-push-$TAG \
       uniqush-build:$TAG
