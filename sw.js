const CACHE = 'nuttywonders-pwa-v11';
const SHELL = [
  '/index.html',
  '/styles.css',
  '/app.js',
  '/product.html',
  '/product.js',
  '/journal.html',
  '/journal.js',
  '/robots.txt',
  '/sitemap.xml',
  '/llms.txt',
  '/admin.html',
  '/admin.css',
  '/admin.js',
  '/qr.js',
  '/manifest-storefront.json',
  '/assets/nuttywonders-icon-192.png',
  '/assets/nuttywonders-icon-512.png',
  '/assets/nuttywonders-logo.png',
  '/assets/brand-slice.webp'
];
self.addEventListener('install', event => {
  event.waitUntil(caches.open(CACHE).then(cache => cache.addAll(SHELL)).then(() => self.skipWaiting()));
});
self.addEventListener('activate', event => {
  event.waitUntil(caches.keys().then(keys => Promise.all(keys.filter(k => k !== CACHE).map(k => caches.delete(k)))).then(() => self.clients.claim()));
});
self.addEventListener('fetch', event => {
  if (event.request.method !== 'GET') return;
  const url = new URL(event.request.url);
  if (url.origin !== self.location.origin) return;
  if (url.pathname.startsWith('/api/')) { event.respondWith(fetch(event.request)); return; }
  if (url.pathname.startsWith('/assets/') || /\.(?:css|js|png|webp|jpg|svg|json)$/.test(url.pathname)) {
    event.respondWith(staleWhileRevalidate(event.request)); return;
  }
  if (event.request.mode === 'navigate') { event.respondWith(networkFirstWithFallback(event.request)); return; }
  event.respondWith(networkFirst(event.request));
});
async function networkFirst(request){ const cache = await caches.open(CACHE); try{ const res = await fetch(request); if(res && res.ok && res.type==='basic') cache.put(request, res.clone()); return res;}catch{ const cached = await cache.match(request); if(cached) return cached; if(request.mode==='navigate') return (await cache.match('/index.html'))||new Response('Offline',{status:503}); return new Response('',{status:503});}}
async function networkFirstWithFallback(request){ const cache = await caches.open(CACHE); try{ const res = await fetch(request); if(res && res.ok) cache.put(request,res.clone()); return res;}catch{ const cached = await cache.match(request); if(cached) return cached; const shell = await cache.match('/index.html'); if(shell) return shell; return new Response('Offline',{status:503});}}
async function staleWhileRevalidate(request){ const cache = await caches.open(CACHE); const cached = await cache.match(request); const fetchPromise = fetch(request).then(res=>{ if(res && res.ok) cache.put(request,res.clone()); return res;}).catch(()=>null); return cached || fetchPromise || new Response('',{status:503});}
