#Push Service Provider
A Push Service Provider provides the push service. However, it is a littel
different from things like C2DM, APNS, etc. It conceptially has two
attributes: service type and sender's id.

Let's take C2DM as an example. Suppose I registered two C2DM sender with
two google account, namely sender1@gmail.com and sender2@gmail.com. Then
we have two push service provider now (not one). Both of them have same
service type (C2DM) and different sender's id.

#Delivery Point
Delivery Point is the really entity which will receive the notification.
One delivery point can associated with one and only one Push Service Provider.

#Service
A Service is provided the the application author. One Service may *use* more
than one Push Service Provider.

#Subscriber
One Subscriber may have more than one Delivery Point.

#Connect Them Togather
OK. Here's the big party. I would rather use an analogue to explain these
concepts.

Suppose you want to subscribe some newspaper. So now you are a _Subscriber_.
And not surprisely, the newspaper company provide a _Service_.

Then what infomation you need to tell the newspaper company so that they can
deliver the newspaper every morning? First, your name to identify youself; and
also your address to which they will deliver. The address you provide is a
_Delivery Point_.

Not so hard, right? Let's take a look from the _Service_'s perspective. Now you
know the _Delivery Point_ and its associated _Subscriber_, you need to deliver
your news to them. You can of course hire some people to do that. But more
commonly, we would like to use some 3rd party service - like UPS, Fedex, etc.
However, in our world, because of the market or other complex issues, one
_Delivery Point_ can only receive mails from one specific company. This means
one _Delivery Point_ can only receive notifications from one specific 
_Push Service Provider_. Then once we know the _Delivery Point_, we need to know
which _Push Service Provider_ we want to use. Moreover, one _Subscriber_ may
register several _Delivery Point_. We also need to know which points want to receive
the news.

Now let's suppose you are running a huge media company which has several
subsidiaries. Each subsidiary publishes their own newspaper. Or, in uniqush
context, this company provides different _Service_. These subsidiaries has
their own connection with logistic companies. Then from your perspective, even
two subsidiary use same logistic company's service, they will be treated as two
_Push Service Provider_

Once we need to delivery some news to a _Subscriber_, we have to specify the
_Service_ because one _Subscriber_ may subscribe more than one _Service_ from
your company.

After knowing the _Subscriber_ and _Service_, we need to check our record to
see which _Delivery Point_ could be use. From the _Delivery Point_, we could
know which logistic company will be used (In uniqush context, it is the service
type -- like C2DM, APNS, etc.). Then from the _Service_ and the logistic
company (service type), we could know which _Push Service Provider_ will be
used.
