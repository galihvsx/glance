// glance service worker (C15T4) — minimal PWA support.
//
// Strategy:
// - Cache-first for the app shell (precache index.html, manifest, icons) and
//   for hashed /assets/* bundles.
// - Network-first for /api/* — never serve stale issue data.
// - Navigations fall back to the cached shell when offline.
//
// Deliberately minimal: no background sync, no push.

const CACHE = "glance-shell-v1";
const PRECACHE = [
  "/",
  "/index.html",
  "/manifest.webmanifest",
  "/icon-192.png",
  "/icon-512.png",
];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((cache) => cache.addAll(PRECACHE))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))),
      )
      .then(() => self.clients.claim()),
  );
});

function isHashedAsset(pathname) {
  return pathname.startsWith("/assets/");
}

self.addEventListener("fetch", (event) => {
  const { request } = event;
  if (request.method !== "GET") return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;

  // Network-first for API traffic.
  if (url.pathname.startsWith("/api/")) {
    event.respondWith(fetch(request).catch(() => caches.match(request)));
    return;
  }

  // Cache-first for the app shell and hashed static assets.
  if (isHashedAsset(url.pathname) || PRECACHE.includes(url.pathname)) {
    event.respondWith(
      caches.match(request).then(
        (hit) =>
          hit ??
          fetch(request).then((res) => {
            const copy = res.clone();
            caches.open(CACHE).then((cache) => cache.put(request, copy));
            return res;
          }),
      ),
    );
    return;
  }

  // Navigations: network first, offline fallback to the cached shell.
  if (request.mode === "navigate") {
    event.respondWith(fetch(request).catch(() => caches.match("/index.html")));
  }
});
