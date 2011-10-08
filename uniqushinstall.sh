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
    goinstall github.com/monnand/uniqush/uniqush
    compile_and_install
}

function uninstall
{
    rm -rf ${GOROOT}/src/pkg/github.com/monnand/uniqush
    rm -rf ${GOROOT}/pkg/${GOOS}_${GOARCH}/github.com/monnand/uniqush
}

function update
{
    goinstall -u -v github.com/monnand/uniqush/uniqush
    compile_and_install
}

function usage
{
    echo "uniqushinstall.sh [install|uninstall|update]"
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
        echo "Updating Uniqush..."
        update
    else
        echo "Installing Uniqush..."
        install
    fi
else
    case $1 in
        "install")
            echo "Installing Uniqush..."
            install
            ;;
        "uninstall")
            echo "Uninstalling..."
            uninstall
            ;;
        "update")
            echo "Updating Uniqush..."
            update
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

