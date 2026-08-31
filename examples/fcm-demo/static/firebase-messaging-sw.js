// Service worker for the FCM demo.
//
// This is where a push lands when the page is not focused. FCM delivers to the
// browser's push service, the browser wakes this worker, and the Firebase SDK
// turns that into onBackgroundMessage.
//
// The file has to be served from the root scope and at exactly this name: the
// Firebase SDK registers /firebase-messaging-sw.js by default, and a worker
// under /static/ could only receive pushes for /static/*.

// The compat build is used deliberately. Service workers cannot use ES module
// imports without a build step, and importScripts needs a classic script.
importScripts('https://www.gstatic.com/firebasejs/10.12.2/firebase-app-compat.js');
importScripts('https://www.gstatic.com/firebasejs/10.12.2/firebase-messaging-compat.js');

// Served by the demo from the same config file the page reads, so the project
// details live in exactly one place.
importScripts('/firebase-config.js');

firebase.initializeApp(self.FIREBASE_CONFIG);
const messaging = firebase.messaging();

// The demo sends data-only messages, so nothing is displayed unless this
// handler displays it. That is the point: what appears on screen is uniqush's
// payload, having survived the whole path, rather than something the browser
// rendered from a "notification" block on its own.
messaging.onBackgroundMessage((payload) => {
  const data = payload.data || {};
  const title = data.title || 'uniqush-push';
  const body = data.body || JSON.stringify(data);

  // Forward to any open tab as well, so the page's log shows background pushes
  // rather than only foreground ones.
  self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clients) => {
    for (const client of clients) {
      client.postMessage({ source: 'sw', data });
    }
  });

  return self.registration.showNotification(title, {
    body,
    // A stable tag would collapse repeated test pushes into one, which hides
    // exactly the thing being tested.
    tag: 'uniqush-' + Date.now(),
  });
});
