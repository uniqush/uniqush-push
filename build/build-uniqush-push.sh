#!/bin/bash -xeu
# Depends on 'nfpm' (https://nfpm.goreleaser.com) being installed.
# Packaging itself is config-driven -- see nfpm.yaml next to this script.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_DIR="`pwd`"

TEMP=`pwd`/tmpgopath

if [ -d "$TEMP" ]; then
	rm -rf "$TEMP"
fi
mkdir -p $TEMP

pushd $TEMP
git clone --depth 1 git@github.com:uniqush/uniqush-push.git
pushd uniqush-push
go get
go build
popd
popd

VERSION=`$TEMP/uniqush-push/uniqush-push --version | sed 's/uniqush-push //'`
BUILD=`pwd`/"uniqush-push-$VERSION"
mkdir -p "$BUILD/usr/bin"
mkdir -p "$BUILD/etc/uniqush/"

ARCH="`uname -m`"

cp "$TEMP/uniqush-push/uniqush-push" "$BUILD/usr/bin"
cp "$TEMP/uniqush-push/conf/uniqush-push.conf" "$BUILD/etc/uniqush"
cp "$TEMP/uniqush-push/LICENSE" LICENSE

export VERSION ARCH
pushd "$BUILD"
nfpm package -f "$SCRIPT_DIR/nfpm.yaml" -p rpm -t "$OUT_DIR"
nfpm package -f "$SCRIPT_DIR/nfpm.yaml" -p deb -t "$OUT_DIR"
popd

TARBALLNAME="uniqush-push_${VERSION}_$ARCH"
TARBALLDIR=`pwd`/"$TARBALLNAME"
mkdir -p "$TARBALLDIR"
cp LICENSE "$TARBALLDIR"
cp "$TEMP/uniqush-push/uniqush-push" "$TARBALLDIR"
cp "$TEMP/uniqush-push/conf/uniqush-push.conf" "$TARBALLDIR/uniqush-push.conf"

cat > "$TARBALLNAME/install.sh" << EOF
#!/bin/sh
mkdir -p /etc/uniqush
cp uniqush-push /usr/local/bin
cp uniqush-push.conf /etc/uniqush
echo "Success!"
EOF

chmod +x "$TARBALLDIR/install.sh"
tar czvf "$TARBALLNAME.tar.gz" "$TARBALLNAME"

rm -rf "$TEMP"
rm -rf "$BUILD"
rm -rf "$TARBALLDIR"
rm -f uniqush-push
rm -f uniqush-push.conf
rm -f LICENSE

echo "Packages are found in uniqush-push_${VERSION}...rpm/.deb/.tar.gz"
