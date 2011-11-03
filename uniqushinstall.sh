#!/bin/bash

function compile_and_install
{
    PWD=`pwd`
    cd ${GOROOT}/src/pkg/github.com/monnand/uniqush
    gomake clean
    gomake && gomake install
    cd $PWD
}

function install
{
    echo "Installing Uniqush..."
    goinstall github.com/monnand/uniqush/uniqush
    compile_and_install
}

function uninstall
{
    echo "Uninstalling..."
    rm -rf ${GOROOT}/src/pkg/github.com/monnand/uniqush
    rm -rf ${GOROOT}/pkg/${GOOS}_${GOARCH}/github.com/monnand/uniqush
}

function update
{
    echo "Updating Uniqush..."
    goinstall -u -v -clean -nuke github.com/monnand/uniqush/uniqush
    compile_and_install
}

function usage
{
    echo "uniqushinstall.sh [install|uninstall|update|config]"
}

function copyconfig
{
    echo "Generating Configuration File to /etc/uniqush/uniqush.conf..."
    mkdir -p /etc/uniqush
    echo """logfile=/var/log/uniqush
[WebFrontend]
log=on
loglevel=standard
address=localhost:9898

[AddPushServiceProvider]
log=on
loglevel=standard

[RemovePushServiceProvider]
log=on
loglevel=standard

[Subscribe]
log=on
loglevel=standard

[Unsubscribe]
log=on
loglevel=standard

[Push]
log=on
loglevel=standard

[Database]
engine=redis
port=0
name=0
everysec=600
leastdirty=10
cachesize=1024""" > /etc/uniqush/uniqush.conf
}

function ifinstalled
{
    if test -d ${GOROOT}/src/pkg/github.com/monnand/uniqush
    then
        return 0
    fi
    return 1

}

if test 0 -eq $#
then
    if ifinstalled
    then
        update
    else
        install
    fi
else
    case $1 in
        "install")
            install
            ;;
        "uninstall")
            uninstall
            ;;
        "update")
            update
            ;;
        "config")
            copyconfig
            ;;
        "help")
            usage
            ;;
        *)
            echo "Unkown argument"
            usage
            ;;
    esac
fi

