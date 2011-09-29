Add Push Service Provider
===========================

- C2DM:
`curl http://127.0.0.1:9898/addpsp -d service=myservice -d pushservicetype=c2dm -d senderid=uniqush.go@gmail.com -d authtoken=faketoken`
- APNS:
`curl http://127.0.0.1:9898/addpsp -d service=myservice -d pushservicetype=apns -d cert=/path/to/certificate.pem -d key=/path/to/privatekey.pem`
`curl http://127.0.0.1:9898/addpsp -d service=myservice -d pushservicetype=apns -d cert=/path/to/certificate.pem -d key=/path/to/privatekey.pem -d sandbox=true`

Remove Push Service Provider
===========================

`curl http://127.0.0.1:9898/rmpsp -d service=myservice -d pushservicetype=c2dm -d senderid=uniqush.go@gmail.com`

Or

`curl http://127.0.0.1:9898/rmpsp -d service=myservice -d pushserviceid=pushserviceid`

Subscribe a Service
===========================
- C2DM:
`curl http://127.0.0.1:9898/subscribe -d service=myservice -d subscriber=uniqush.client -d os=android -d account=uniqush.client@gmail.com -d regid=fakeregid`
- APNS:
`curl http://127.0.0.1:9898/subscribe -d service=myservice -d subscriber=uniqush.client -d os=ios -d devtoken=devtoken`

Unsubscribe a Service
===========================
`curl http://127.0.0.1:9898/unsubscribe -d service=myservice -d subscriber=uniqush.client -d os=android -d account=uniqush.client@gmail.com -d regid=fakeregid`

Or

`curl http://127.0.0.1:9898/unsubscribe -d service=myservice -d subscriber=uniqush.client -d deliverypointid=deliverypointid`

Stop the Program
===========================
`curl http://127.0.0.1:9898/stop`

Push a Notification
==========================
- URL: /push
- Parameters:
  + service: Required.
  + subscriber: Required. Comma seperated. Could be more than one subscriber
  + msg: Required. Message body
  + badge: Optional. Badge
  + img: Optional. Image
  + sound: Optional. Sound

`curl http://127.0.0.1:9898/push -d service=myservice -d subscriber=uniqush.client -d msg="Hello World"`

