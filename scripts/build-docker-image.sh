#!/bin/bash

set -eu

TAG=$( git describe --always --dirty )

# Building binary
docker build --tag uniqush-push-builder:$TAG --file Dockerfile.build .

SCRIPT_DIR="$( cd "$( dirname "$0" )" && pwd )"
DOCKER_DIR="${SCRIPT_DIR}/../docker"
ROOTFS_DIR="${DOCKER_DIR}/rootfs"
CONFIG_FILE="${SCRIPT_DIR}/../conf/uniqush-push.conf"

mkdir -p "${ROOTFS_DIR}/usr/bin" "${ROOTFS_DIR}/etc/uniqush"

# Copying built binary to host
docker run --rm -v "${ROOTFS_DIR}/usr/bin:/tmp/output" uniqush-push-builder:$TAG cp /tmp/bin/uniqush-push /tmp/output

# Copying conf
if [ -f "${CONFIG_FILE}.custom" ]; then
    CONFIG_FILE="${CONFIG_FILE}.custom"
fi
cp "${CONFIG_FILE}" "${ROOTFS_DIR}/etc/uniqush/uniqush-push.conf"

# Copying CA certificates
mkdir -p "${ROOTFS_DIR}/etc/ssl/certs"
cp "${DOCKER_DIR}/certificates/"* "${ROOTFS_DIR}/etc/ssl/certs/"

# Updating conf to be Docker-friendly
sed -i '' \
    -e 's!^\(addr\)=.*:\(.*\)$!\1=0.0.0.0:\2!' \
    -e 's!/var/log/uniqush$!/dev/stdout!' \
    -e $'s!engine=redis!&\\\nhost=redis!' \
    "${ROOTFS_DIR}/etc/uniqush/uniqush-push.conf"

# Building slim docker image
docker build --tag uniqush-build:$TAG --file Dockerfile .

docker rmi uniqush-push-builder:$TAG
rm -rf "${ROOTFS_DIR}"

echo
echo ">> Successfully built docker image 'uniqush-build:$TAG'"
echo
docker images uniqush-build:$TAG
