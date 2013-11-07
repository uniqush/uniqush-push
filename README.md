- [Homepage](http://uniqush.org)
- [Download](http://uniqush.org/downloads.html)
- [Blog/News](http://blog.uniqush.org)
- [@uniqush](http://twitter.com/uniqush)

# Introduction #

*Uniqush* is a _free_ and _open source_ software which provides a unified push
service for server-side notification to apps on mobile devices. By running
*uniqush* on server side, you can push notification to any supported mobile
platform.

# Supported Platforms #

- [GCM](http://developer.android.com/guide/google/gcm/index.html) from google for android platform
- [APNS](http://developer.apple.com/library/mac/#documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/ApplePushService/ApplePushService.html) from apple for iOS platform
- [ADM](https://developer.amazon.com/sdk/adm.html) from amazon for Kindle tables
- [C2DM](https://developers.google.com/android/c2dm/) from google for android platform (deprecated by google. using [GCM](http://developer.android.com/guide/google/gcm/index.html) instead.)

# FAQ #

- Q: A general push notification platform for all systems? How does this differ
  from services like [urbanairship](http://urbanairship.com)?
- A: [urbanairship](http://urbanairship.com) is a great service, and there're
  other similar services available, like [openpush](http://openpush.im/),
[notifica](https://notifica.re/), etc. All of them are wonderful services.
However, [uniqush](http://uniqush.org) is different from them.
[Uniqush](http://uniqush.org) is not a service. Instead,
**[Uniqush](http://uniqush.org) is a system, which runs on your own
server**. In fact, if you wish, you can use uniqush to setup a similar service like [urbanairship](http://urbanairship.com).

- Q: OK. Then is it a library? Like
  [java-apns](https://github.com/notnoop/java-apns)?
- A: Well.. Not actually. I mean, it is a program, like apache httpd. You
  download it, you run it, that's it. You don't need to worry about which
language to use, package dependencies, etc. 

- Q: But wait, how can I use it anyway? I mean, if my program wants to push
  something, I need to tell uniqush about this action. How can I communicate
with uniqush? There must be some library so that I can use it in my
program to talk with uniqush, right?
- A: We are trying to make it easier. *uniqush-push* provides RESTful APIs. In
  other words, you talk with *uniqush-push* through HTTP protocol. As long as
there's an HTTP client library for your language, you can use it and talk with
*uniqush-push*. For details about the our RESTful APIs, you may want to check
out our [document](http://uniqush.org/documentation/index.html).

- Q: Then that's cool. But I noticed that you are using [Go](http://golang.org) programming language. Do I need to install [Go](http://golang.org) compiler and other stuff to run *uniqush-push*?
- A: No. There is **no** dependency. All you need to do is to download the
  binary file from the [download page](http://uniqush.org/downloads.html) and
install it. You only need to have a [redis](http://redis.io) database running
somewhere so that *uniqush-push* can store the user data in
[redis](http://redis.io). For more details, here is the [installation guide](http://uniqush.org/documentation/install.html)

- Q: This is nice. I want to give it a try. But you are keep talking about *uniqush-push*, and I'm talking about *uniqush*, are they the same thing?
- A: Thank you for your support! *Uniqush* is intended to be the name of a
  system which provides a full stack solution for communication between mobile
devices and the app's server. *uniqush-push* is one piece of the system.
However, right now, *uniqush-push* is the only piece and others are under
actively devlopment. If you want to know more details about the *uniqush*
system's plan, you can read the [blog
post](http://blog.uniqush.org/uniqush-after-go1.html). If you want to know the
latest progress about *uniqush* system, please check [our
blog](http://blog.uniqush.org/). And if you are really impatient, [our github
account](http://github.com/uniqush) will always contain the latest progress.

# Related Links #
- [This story](http://uniqush.org/documentation/intro.html) may help you to understand
the basic idea of *uniqush*.
- [Documentation](http://uniqush.org/documentation/index.html)
- [Uniqush Blog](http://blog.uniqush.org) announces the latest news about uniqush.

