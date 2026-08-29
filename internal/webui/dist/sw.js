// Minimal hand-written service worker — no Workbox/vite-plugin-pwa, to
// keep the dependency footprint small (single-maintainer project).
const CACHE_NAME = "bitacora-v1";
const PRECACHE_URLS = ["/", "/manifest.webmanifest", "/icon-192.png", "/icon-512.png", "/icon-180.png"];

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(CACHE_NAME).then((cache) => cache.addAll(PRECACHE_URLS)));
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k))))
      .then(() => clients.claim()),
  );
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  const url = new URL(req.url);

  // Live monitoring data must never be served stale from cache.
  if (req.method !== "GET" || url.origin !== self.location.origin || url.pathname.startsWith("/v1/")) {
    return;
  }

  event.respondWith(
    fetch(req)
      .then((res) => {
        const copy = res.clone();
        caches.open(CACHE_NAME).then((cache) => cache.put(req, copy));
        return res;
      })
      .catch(() => caches.match(req)),
  );
});
