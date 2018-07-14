#!/bin/bash
# Note: this test script should work with apns-simulator.
# To install apns-simulator:
# go get github.com/uniqush/apns-simulator
# Then run it with the command apns-simulator (Must have cert.pem from the apns-simulator folder in the current working directory)

PWD=`pwd`
PAYLOAD='{"aps":{"alert":{"body":"Hello World"}},"key1":{}}'

curl http://127.0.0.1:9898/addpsp -d service=myservice -d pushservicetype=apns -d cert=$PWD/localhost.cert -d key=$PWD/localhost.key -d addr=127.0.0.1:8080 -d skipverify=true

curl http://127.0.0.1:9898/subscribe -d service=myservice -d subscriber=uniqush.client -d pushservicetype=apns -d devtoken=3df3e210e7adf35f840f45b269b760d9d51081569dc4509ee98bb4d4c92a828e
curl http://127.0.0.1:9898/subscribe -d devid=AFAKEDEVICEID -d service=myservice -d subscriber=uniqush.client2 -d pushservicetype=apns -d devtoken=55f3e210e7adf35f840f45b269b760d9d51081569dc4509ee98bb4d4c92a828e
curl http://127.0.0.1:9898/subscriptions -d service=myservice -d subscriber=uniqush.client
curl http://127.0.0.1:9898/subscriptions -d service=myservice -d subscriber=uniqush.client -d include_delivery_point_ids=1
# Delivery point id hash is based on service name and subscriber, should be reproducible.
curl http://127.0.0.1:9898/push -d service=myservice -d subscriber=uniqush.client -d delivery_point_id="apns:86e4809a2f44f2e087804ad61a4c6424809606b5" -d uniqush.payload.apns="$PAYLOAD"
curl http://127.0.0.1:9898/subscriptions -d service=myservice -d subscriber=uniqush.client2
curl http://127.0.0.1:9898/rebuildserviceset; echo
curl http://127.0.0.1:9898/subscriptions -d subscriber=uniqush.client
curl http://127.0.0.1:9898/psps


echo
successful_curl (){
	# TODO: This seems like it's gotten faster?
	curl http://127.0.0.1:9898/push -d service=myservice -d subscriber=uniqush.client -d uniqush.payload.apns="$PAYLOAD"
}

# 3 tests of push: no push type, no apns id, and successful push.
curl http://127.0.0.1:9898/push -d service=myservice -d subscriber=uniqush.client -d uniqush.payload.apns='{"aps":{"alert":{"body":"Hello World"}},"key1":{}}'
curl http://127.0.0.1:9898/push -d service=myservice -d subscriber=uniqush.client -d uniqush.payload.apns="$PAYLOAD"
successful_curl

for i in {1..10}
do
	successful_curl &
done

wait
