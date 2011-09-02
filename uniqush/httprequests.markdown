Add Push Service Provider
===========================

`curl http://127.0.0.1:9898/addpsp -d service=myservice -d pushservicetype=c2dm -d senderid=monnand@gmail.com -d authtoken=faketoken`

Subscribe a Service
===========================
`curl http://127.0.0.1:9898/subscribe -d service=myservice -d subscriber=monnand@gmail.com -d os=android -d account=monnand@gmail.com -d regid=fakeregid`

