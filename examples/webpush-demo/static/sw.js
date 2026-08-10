// Service worker for the uniqush-push Web Push demo.
//
// The browser decrypts the RFC 8291 payload before this runs, so event.data is
// already plaintext: the JSON that uniqush's webpush backend built from the
// /push parameters, minus its own uniqush.* control keys.

self.addEventListener('install', (event) => {
  // Take over immediately rather than waiting for existing tabs to close,
  // which during testing means every reload would otherwise use the old worker.
  event.waitUntil(self.skipWaiting());
});

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener('push', (event) => {
  let payload = {};
  if (event.data) {
    try {
      payload = event.data.json();
    } catch (err) {
      // A push sent with uniqush.payload.webpush can carry anything, including
      // a bare string or a wakeup ping with no useful body.
      payload = { body: event.data.text() };
    }
  }

  const title = payload.title || 'uniqush-push';
  const body = payload.body || payload.msg || JSON.stringify(payload);

  event.waitUntil((async () => {
    // Chrome requires a visible notification for every push when the
    // subscription was created with userVisibleOnly, and will show a generic
    // "site updated in the background" one if we do not.
    await self.registration.showNotification(title, {
      body,
      tag: 'uniqush-demo',
      // Without this, two pushes with the same tag replace each other silently.
      renotify: true,
    });

    // Echo to any open tab so the page's log shows what actually arrived. This
    // is what distinguishes "delivered but not displayed" from "never arrived".
    const clients = await self.clients.matchAll({
      type: 'window',
      includeUncontrolled: true,
    });
    for (const client of clients) {
      client.postMessage({ type: 'push-received', payload });
    }
  })());
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  event.waitUntil((async () => {
    const clients = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    for (const client of clients) {
      if ('focus' in client) return client.focus();
    }
    if (self.clients.openWindow) return self.clients.openWindow('/');
  })());
});
