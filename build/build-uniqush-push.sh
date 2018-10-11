#!/bin/bash -xeu
# Depends on the ruby gem "fpm" being installed.
# Exits immediately on error.

TEMP=`pwd`/tmpgopath
LICENSE=Apache-2.0

mkdir -p "$TEMP/bin"
mkdir -p "$TEMP/src"
mkdir -p "$TEMP/pkg"

if [ -d "$TEMP" ]; then
	rm -rf "$TEMP"
fi

GOBIN="$TEMP/bin" GOPATH="$TEMP" go get github.com/uniqush/uniqush-push

VERSION=`"$TEMP/bin/uniqush-push" --version | sed 's/uniqush-push //'`

BUILD=`pwd`/"uniqush-push-$VERSION"
mkdir -p "$BUILD/usr/bin"
mkdir -p "$BUILD/etc/uniqush/"
mkdir -p "$BUILD/systemd/"
mkdir -p "$BUILD/systemvinit/"

ARCH="`uname -m`"

cp "$TEMP/bin/uniqush-push" "$BUILD/usr/bin"
cp "$TEMP/src/github.com/uniqush/uniqush-push/conf/uniqush-push.conf" "$BUILD/etc/uniqush"
cp "../conf/systemd/uniqush-push.service" "$BUILD/systemd/uniqush-push.service"
cp "../conf/systemvinit/uniqush.init" "$BUILD/systemvinit/uniqush-push"
cp "$TEMP/src/github.com/uniqush/uniqush-push/LICENSE" "$LICENSE"

fpm -s dir -t rpm -v "$VERSION" -n uniqush-push --license="$LICENSE" --maintainer="Nan Deng" --vendor "uniqush" --url="http://uniqush.org" --category Network --description "Uniqush is a free and open source software which provides a unified push service for server-side notification to apps on mobile devices" -a "$ARCH" -C "$BUILD" .

fpm -s dir -t deb -v "$VERSION" -n uniqush-push --license="$LICENSE" --maintainer="Nan Deng" --vendor "uniqush" --url="http://uniqush.org" --deb-systemd="$BUILD/systemd/uniqush-push.service" --deb-init="$BUILD/systemvinit/uniqush-push" --category Network --description "Uniqush is a free and open source software which provides a unified push service for server-side notification to apps on mobile devices" -a "$ARCH" -C "$BUILD" .

TARBALLNAME="uniqush-push_${VERSION}_$ARCH"
TARBALLDIR=`pwd`/"$TARBALLNAME"
mkdir -p "$TARBALLDIR"
cp "$LICENSE" "$TARBALLDIR"
cp "$TEMP/bin/uniqush-push" "$TARBALLDIR"
cp "$TEMP/src/github.com/uniqush/uniqush-push/conf/uniqush-push.conf" "$TARBALLDIR/uniqush-push.conf"

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
rm -f "$LICENSE"

echo "Packages are found in uniqush-push_${VERSION}...rpm/.deb/.tar.gz"
