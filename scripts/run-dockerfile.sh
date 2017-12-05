#!/bin/bash -e

TAG=$( git describe --always )

function finish {
    echo "Removing container..."
    docker rm uniqush-push-$TAG
}

trap finish EXIT

SCRIPT_DIR="$( cd "$( dirname "$0" )" && pwd )"
bash "${SCRIPT_DIR}/build-docker-image.sh"

docker run \
       --name=uniqush-push-$TAG \
       uniqush-build:$TAG
