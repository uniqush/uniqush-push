#!/bin/sh

TEMP=`pwd`/tmpgopath

mkdir -p $TEMP/bin
mkdir -p $TEMP/src
mkdir -p $TEMP/pkg

GOBIN=$TEMP/bin GOPATH=$TEMP go get github.com/monnand/uniqush/uniqush-push

VERSION=`$TEMP/bin/uniqush-push --version | sed 's/uniqush-push //'`

CTLSCRIPT=`pwd`/uniqush-push.ctl

cat > $CTLSCRIPT << DELIM
# This is a generated script
Section: Network
Priority: optional
Homepage: http://uniqush.org
Standards-Version: 3.9.2

Package: uniqush-push
Version: $VERSION
Maintainer: Nan Deng <monnand@gmail.com>
Architecture: amd64
Copyright: LICENSE
Files: uniqush-push /usr/bin/
 uniqush-push.conf /etc/uniqush/
Description: Push notification solution for all mobile platforms
DELIM

cp $TEMP/bin/uniqush-push .
cp $TEMP/src/github.com/monnand/uniqush/conf/uniqush-push.conf .
cp $TEMP/src/github.com/monnand/uniqush/LICENSE .

equivs-build $CTLSCRIPT

rm -rf $TEMP
rm -f $CTLSCRIPT
rm -f uniqush-push
rm -f uniqush-push.conf
rm -f LICENSE

