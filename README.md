- [Homepage](http://uniqush.org)
- [Download](http://uniqush.org/downloads.html)
- [Blog/News](http://blog.uniqush.org)
- [@uniqush](http://twitter.com/uniqush)

## Introduction ##

*Uniqush* (\ˈyü-nə-ku̇sh\ "uni" pronounced as in "unified", and "qush" pronounced as
in "cushion") is a _free_ and _open source_ software system which provides
a unified push service for server side notification to apps on mobile devices.
The `uniqush-push` API abstracts the APIs of the various push services used
to send push notifications to those devices. By running `uniqush-push` on the
server side, you can send push notifications to any supported mobile platform.

[![Build Status](https://travis-ci.org/uniqush/uniqush-push.svg?branch=master)](https://travis-ci.org/uniqush/uniqush-push)

## Supported Platforms ##

- [GCM](http://developer.android.com/guide/google/gcm/index.html) from Google for the Android platform
- [APNS](http://developer.apple.com/library/mac/#documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/ApplePushService/ApplePushService.html) from Apple for the iOS platform
- [ADM](https://developer.amazon.com/sdk/adm.html) from Amazon for Kindle tablets

## FAQ ##

- Q: Is this a general push notification platform for all types of devices? How does this differ
  from services such as [Urban Airship](http://urbanairship.com)?
- A: [Urban Airship](http://urbanairship.com) is a great service, and there are
  other similar services available, like [OpenPush](http://openpush.im/),
[Notificare](https://notifica.re/), etc. All of them are wonderful services.
However, [Uniqush](http://uniqush.org) is different from them.
[Uniqush](http://uniqush.org) is not a service. Instead,
**[Uniqush](http://uniqush.org) is a system, which runs on your own
server**. In fact, if you wish, you can use Uniqush to setup a service similar to [Urban Airship](http://urbanairship.com).

- Q: OK. Then is it a library? Like
  [java-apns](https://github.com/notnoop/java-apns)?
- A: Well.. Not actually. I mean, it is a program, like Apache HTTP Server. You download it, you run it. It does require a [Redis](http://redis.io/) server, but, other than that, you don't need to worry about which language to use, package dependencies, etc.

- Q: But wait, how can I use it anyway? I mean, if my program wants to send
  a push notification, I need to tell Uniqush about this action. How can I
  communicate with Uniqush? There must be some library so that I can use it
  in my program to talk with Uniqush, right?
- A: We are trying to make it easier. `uniqush-push` provides RESTful APIs. In
  other words, you talk with `uniqush-push` through HTTP protocol. As long as
there's an HTTP client library for your language, you can use it and talk with
`uniqush-push`. For details about the our RESTful APIs, see [our API
documentation](http://uniqush.org/documentation/usage.html).

- Q: Then that's cool. But I noticed that you are using [Go](http://golang.org) programming language. Do I need to install [Go](http://golang.org) compiler and other stuff to run `uniqush-push`?
- A: No. There are no installation dependencies. All you need to do is to download the
  binary file from the [download page](http://uniqush.org/downloads.html) and
install it. But you do need to set up a [Redis](http://redis.io) server running
somewhere, preferably with persistence, so that `uniqush-push` can store the
user data in [Redis](http://redis.io). For more details, see the
[installation guide](http://uniqush.org/documentation/install.html)

- Q: This is nice. I want to give it a try. But you are keep talking about `uniqush-push`, and I'm talking about *Uniqush*, are they the same thing?
- A: Thank you for your support! *Uniqush* is intended to be the name of a
  system which provides a full stack solution for communication between mobile
devices and the app's server. `uniqush-push` is one piece of the system.
However, right now, `uniqush-push` is the only piece and others are under
active development. If you want to know more details about the *Uniqush*
system's plan, you can read the [blog
post](http://blog.uniqush.org/uniqush-after-go1.html). If you want to find out
about the latest progress with *Uniqush*, please check out [our
 blog](http://blog.uniqush.org/). And, if you are really impatient, there's
 always our [our GitHub account](http://github.com/uniqush) which could have
 brand new stuff that hasn't been released yet.

## Setting Up Redis ##

[Redis persistence](http://redis.io/topics/persistence) describes the details
of how Redis saves data on shutdown, as well as how one might back up that
data. Make sure that the Redis server you use has persistence enabled - your
redis.conf should have contents similar to the section `**PERSISTENCE**` of
redis.conf in the example config files linked in http://redis.io/topics/config

## Contributing ##

You're encouraged to contribute to the `uniqush-push` project. There are two ways you can contribute.

### Issues ###

If you encounter an issue while using `uniqush-push`, please report it at the project's [issues tracker](https://github.com/uniqush/uniqush-push/issues). Feature suggestions are also welcome.

### Pull request ###

Code contributions to `uniqush-push` can be made using pull requests. To submit a pull request:

1. Fork this project.
2. Make and commit your changes.
3. Submit your changes as a pull request.

## Related Links ##
- [This story](http://uniqush.org/documentation/intro.html) may help you to understand
the basic idea of *Uniqush*.
- [Documentation](http://uniqush.org/documentation/index.html)
- [The Uniqush blog](http://blog.uniqush.org) announces the latest news about Uniqush.
- [Redis persistence](http://redis.io/topics/persistence)
