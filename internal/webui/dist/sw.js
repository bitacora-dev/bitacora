// Minimal hand-written service worker — no Workbox/vite-plugin-pwa, to
// keep the dependency footprint small (single-maintainer project).
//
// Never cache the HTML shell. Vite gives JavaScript and CSS content-hashed
// filenames, so an old cached index.html can point at assets the next deploy
// has removed and leave the dashboard blank. The page must always come from
// the network; only stable, version-independent public assets are precached.
const CACHE_NAME = "bitacora-shell-v2";
const PRECACHE_URLS = ["/manifest.webmanifest", "/icon-192.png", "/icon-512.png", "/icon-180.png"];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(CACHE_NAME)
      .then((cache) => cache.addAll(PRECACHE_URLS))
      .then(() => self.skipWaiting()),
  );
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

  // Live monitoring data and navigations must never be served stale. In
  // particular, keeping a cached navigation response would restore an HTML
  // shell whose hashed asset URLs may no longer exist after a deploy.
  if (
    req.method !== "GET" ||
    url.origin !== self.location.origin ||
    url.pathname.startsWith("/v1/") ||
    req.mode === "navigate"
  ) {
    return;
  }

  if (!PRECACHE_URLS.includes(url.pathname)) return;

  event.respondWith(
    caches.match(req).then((cached) => cached ?? fetch(req)),
  );
});
