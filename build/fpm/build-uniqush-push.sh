#!/bin/sh

TEMP=`pwd`/tmpgopath

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
cp $TEMP/src/github.com/monnand/uniqush/LICENSE APACHE

fpm -s dir -t deb -v $VERSION -n uniqush-push --license=APACHE --maintainer="Nan Deng" --vendor "uniqush" --url="http://uniqush.org" $BUILD

fpm -s dir -t rpm -v $VERSION -n uniqush-push --license=APACHE --maintainer="Nan Deng" --vendor "uniqush" --url="http://uniqush.org" $BUILD

rm -rf $TEMP
rm -rf $BUILD
rm -f uniqush-push
rm -f uniqush-push.conf
rm -f APACHE

