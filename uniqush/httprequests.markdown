Add Push Service Provider
===========================

`curl http://127.0.0.1:9898/addpsp -d service=myservice -d pushservicetype=c2dm -d senderid=monnand@gmail.com -d authtoken=faketoken`

Remove Push Service Provider
===========================

`curl http://127.0.0.1:9898/rmpsp -d service=myservice -d pushservicetype=c2dm -d senderid=monnand@gmail.com -d authtoken=faketoken`

Or

`curl http://127.0.0.1:9898/rmpsp -d service=myservice -d pushserviceid=pushserviceid`

Subscribe a Service
===========================
`curl http://127.0.0.1:9898/subscribe -d service=myservice -d subscriber=monnand -d os=android -d account=monnand@gmail.com -d regid=fakeregid`

Unsubscribe a Service
===========================
`curl http://127.0.0.1:9898/unsubscribe -d service=myservice -d subscriber=monnand -d os=android -d account=monnand@gmail.com -d regid=fakeregid`

Or

`curl http://127.0.0.1:9898/unsubscribe -d service=myservice -d subscriber=monnand -d deliverypointid=deliverypointid`

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

`curl http://127.0.0.1:9898/push -d service=myservice -d subscriber=monnand -d msg="Hello World"`

