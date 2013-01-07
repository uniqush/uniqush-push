#!/bin/sh
# Note: this test script should work with apns-simulator.
# To install apns-simulator:
# go get github.com/uniqush/apns-simulator
# Then run it with the command apns-simulator

PWD=`pwd`

curl http://127.0.0.1:9898/addpsp -d service=myservice -d pushservicetype=apns -d cert=$PWD/localhost.cert -d key=$PWD/localhost.key -d addr=127.0.0.1:8080 -d skipverify=true

curl http://127.0.0.1:9898/subscribe -d service=myservice -d subscriber=uniqush.client -d pushservicetype=apns -d devtoken=3df3e210e7adf35f840f45b269b760d9d51081569dc4509ee98bb4d4c92a828e

curl http://127.0.0.1:9898/push -d service=myservice -d subscriber=uniqush.client -d msg="Hello World"
