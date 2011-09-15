#!/bin/bash
cd uniqush
make clean
make && make install
cd ..
make clean
make
./uniqushd -config=myconf
