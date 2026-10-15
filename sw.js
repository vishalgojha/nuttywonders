const CACHE = 'nuttywonders-pwa-v4';
const SHELL = ['/index.html','/styles.css','/app.js','/admin.html','/admin.css','/admin.js','/qr.js','/order.html','/order.js','/manifest-storefront.json','/manifest-studio.json','/assets/nuttywonders-icon-192.png','/assets/nuttywonders-icon-512.png'];
self.addEventListener('install', event => event.waitUntil(caches.open(CACHE).then(cache => cache.addAll(SHELL)).then(() => self.skipWaiting())));
self.addEventListener('activate', event => event.waitUntil(caches.keys().then(keys => Promise.all(keys.filter(key => key !== CACHE).map(key => caches.delete(key)))).then(() => self.clients.claim())));

// Network-first everywhere so a deploy is visible immediately; the cache is
// only there to keep the app usable offline. The previous cache-first handler
// happily served a week-old app.js, which is how visitors ended up on a
// bundle whose buttons did nothing.
self.addEventListener('fetch', event => {
  if (event.request.method !== 'GET') return;
  const url = new URL(event.request.url);
  if (url.origin !== self.location.origin) return;
  // API responses must never be cached. The WhatsApp pairing code is a live
  // credential that rotates every ~20 seconds; serving a stale one from
  // cache leaves the Studio showing dead text and makes pairing impossible.
  if (url.pathname.startsWith('/api/')) {
    event.respondWith(fetch(event.request));
    return;
  }
  event.respondWith(networkFirst(event.request));
});

async function networkFirst(request) {
  const cache = await caches.open(CACHE);
  try {
    const response = await fetch(request);
    if (response && response.ok && response.type === 'basic') {
      cache.put(request, response.clone());
    }
    return response;
  } catch {
    const cached = await cache.match(request);
    if (cached) return cached;
    if (request.mode === 'navigate') {
      const shell = await cache.match('/index.html');
      if (shell) return shell;
    }
    return new Response('Offline', { status: 503, statusText: 'Offline' });
  }
}
