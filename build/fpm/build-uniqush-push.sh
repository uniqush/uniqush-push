#!/bin/sh

TEMP=`pwd`/tmpgopath
LICENSE=Apache-2.0

mkdir -p $TEMP/bin
mkdir -p $TEMP/src
mkdir -p $TEMP/pkg

GOBIN=$TEMP/bin GOPATH=$TEMP go get github.com/monnand/uniqush/uniqush-push

VERSION=`$TEMP/bin/uniqush-push --version | sed 's/uniqush-push //'`

BUILD=`pwd`/uniqush-push-$VERSION
mkdir -p $BUILD/usr/bin
mkdir -p $BUILD/etc/uniqush-push

cp $TEMP/bin/uniqush-push $BUILD/usr/bin
cp $TEMP/src/github.com/monnand/uniqush/conf/uniqush-push.conf $BUILD/etc/uniqush-push
cp $TEMP/src/github.com/monnand/uniqush/LICENSE $LICENSE

TARGETS=(deb rpm)

for target in ${TARGETS[*]}
do
fpm -s dir -t $target -v $VERSION -n uniqush-push --license=$LICENSE --maintainer="Nan Deng" -d redis --vendor "uniqush" --url="http://uniqush.org" --category Network --description "Uniqush is a free and open source software which provides a unified push service for server-side notification to apps on mobile devices" -C $BUILD .
done

rm -rf $TEMP
rm -rf $BUILD
rm -f uniqush-push
rm -f uniqush-push.conf
rm -f $LICENSE 

