/** Loopback-only production assets + real API/WS proxy for browser budget tests. */
import { createServer } from 'node:http';
import { readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { gzipSync } from 'node:zlib';
import { createProxyMiddleware } from 'http-proxy-middleware';

const [distArgument, backend, portArgument] = process.argv.slice(2);
if (!distArgument || !/^http:\/\/127\.0\.0\.1:\d+$/.test(backend ?? '')) {
  throw new Error('usage: node production-assets-server.mjs DIST http://127.0.0.1:BACKEND_PORT PORT');
}
const dist = path.resolve(distArgument);
const types = {
  '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8',
  '.html': 'text/html; charset=utf-8', '.json': 'application/json',
  '.webmanifest': 'application/manifest+json', '.svg': 'image/svg+xml',
  '.png': 'image/png', '.webp': 'image/webp', '.ico': 'image/x-icon',
};
const proxy = createProxyMiddleware({ target: backend, ws: true });
const assets = new Map();

function asset(file) {
  if (!assets.has(file)) {
    const body = readFileSync(file);
    const type = types[path.extname(file)] ?? 'application/octet-stream';
    assets.set(file, { body, type, gzip: /javascript|text\/|json/.test(type) ? gzipSync(body, { level: 9 }) : null });
  }
  return assets.get(file);
}

const server = createServer((request, response) => {
  const url = new URL(request.url, 'http://127.0.0.1');
  if (url.pathname === '/__test/worker' && request.method === 'POST') {
    const chunks = [];
    let bytes = 0;
    request.on('data', (chunk) => {
      bytes += chunk.length;
      if (bytes > 128 * 1024) request.destroy();
      else chunks.push(chunk);
    });
    request.on('end', () => {
      const body = Buffer.concat(chunks);
      assets.set(path.join(dist, 'sw.js'), { body, type: types['.js'], gzip: gzipSync(body, { level: 9 }) });
      response.writeHead(204).end();
    });
    return;
  }
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/themes/')) {
    proxy(request, response);
    return;
  }
  if (!['GET', 'HEAD'].includes(request.method)) {
    response.writeHead(405).end();
    return;
  }
  let pathname;
  try {
    pathname = decodeURIComponent(url.pathname);
  } catch {
    response.writeHead(400).end();
    return;
  }
  const admin = pathname === '/admin' || pathname.startsWith('/admin/') || pathname === '/terminal';
  let relative = pathname.startsWith('/admin/assets/')
    ? `standalone/admin/assets/${pathname.slice('/admin/assets/'.length)}`
    : pathname.replace(/^\/+/, '');
  if (!path.extname(pathname)) relative = admin ? 'standalone/admin/index.html' : 'index.html';
  const file = path.resolve(dist, relative);
  if (!file.startsWith(dist + path.sep) || !statSync(file, { throwIfNoEntry: false })?.isFile()) {
    response.writeHead(404, { 'Content-Type': 'text/plain' }).end('Not found');
    return;
  }
  const { body, type, gzip } = asset(file);
  const compress = gzip && /\bgzip\b/.test(request.headers['accept-encoding'] ?? '');
  const encoded = compress ? gzip : body;
  response.writeHead(200, {
    'Content-Type': type, 'Content-Length': encoded.length, 'Cache-Control': 'no-cache',
    ...(gzip ? { Vary: 'Accept-Encoding' } : {}),
    ...(compress ? { 'Content-Encoding': 'gzip' } : {}),
  });
  response.end(request.method === 'HEAD' ? undefined : encoded);
});
server.on('upgrade', (request, socket, head) => proxy.upgrade(request, socket, head));
server.listen(Number(portArgument), '127.0.0.1', () => console.log(`READY ${server.address().port}`));
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => server.close(() => process.exit(0)));
