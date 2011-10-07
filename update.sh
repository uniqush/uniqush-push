#!/bin/sh
goinstall -u -v github.com/monnand/uniqush/uniqush
gomake clean
gomake
gomake install
