import http from 'node:http';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const root = path.dirname(fileURLToPath(import.meta.url));
const port = Number(process.env.PORT || 4178);
const startedAt = process.hrtime.bigint();
const bootCpu = process.cpuUsage();
const posts = Array.from({ length: 240 }, (_, index) => ({
  id: index + 1, author: ['Ada', 'Linus', 'Grace', 'Edsger'][index % 4], handle: ['ada', 'linus', 'grace', 'edsger'][index % 4],
  text: `Benchmark post ${index + 1}: a small, realistic timeline payload for the runtime comparison.`, likes: (index * 17) % 900, replies: (index * 7) % 90,
}));
const counters = { requests: 0, reads: 0, searches: 0, likes: 0, posts: 0 };
function json(res, status, value) { const body = JSON.stringify(value); res.writeHead(status, { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store', 'content-length': Buffer.byteLength(body) }); res.end(body); }
async function body(req) { let value = ''; for await (const chunk of req) value += chunk; return value ? JSON.parse(value) : {}; }
function metrics() { const cpu = process.cpuUsage(bootCpu); const memory = process.memoryUsage(); return { pid: process.pid, rssBytes: memory.rss, heapUsedBytes: memory.heapUsed, cpuUserMicros: cpu.user, cpuSystemMicros: cpu.system, uptimeMs: Number(process.hrtime.bigint() - startedAt) / 1e6, counters: { ...counters } }; }
const server = http.createServer(async (req, res) => {
  counters.requests++; const url = new URL(req.url, `http://${req.headers.host || 'localhost'}`);
  try {
    if (url.pathname === '/__ready') return json(res, 200, { ready: true });
    if (url.pathname === '/__metrics') return json(res, 200, metrics());
    if (req.method === 'GET' && url.pathname === '/') { const html = await readFile(path.join(root, 'index.html')); res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' }); return res.end(html); }
    if (req.method === 'GET' && url.pathname === '/app.js') { const js = await readFile(path.join(root, 'app.js')); res.writeHead(200, { 'content-type': 'text/javascript; charset=utf-8' }); return res.end(js); }
    if (req.method === 'GET' && url.pathname === '/api/feed') { counters.reads++; const offset = Math.max(0, Number(url.searchParams.get('offset') || 0)); return json(res, 200, { posts: posts.slice(offset % posts.length, (offset % posts.length) + 20) }); }
    if (req.method === 'GET' && url.pathname === '/api/search') { counters.searches++; const query = (url.searchParams.get('q') || '').toLowerCase(); return json(res, 200, { posts: posts.filter((post) => `${post.author} ${post.text}`.toLowerCase().includes(query)).slice(0, 20) }); }
    if (req.method === 'POST' && /^\/api\/posts\/\d+\/like$/.test(url.pathname)) { counters.likes++; const post = posts.find((item) => item.id === Number(url.pathname.split('/')[3])); if (!post) return json(res, 404, { error: 'post not found' }); post.likes++; return json(res, 200, { id: post.id, likes: post.likes }); }
    if (req.method === 'POST' && url.pathname === '/api/posts') { counters.posts++; const input = await body(req); if (!input.text || typeof input.text !== 'string') return json(res, 400, { error: 'text is required' }); const post = { id: posts.length + 1, author: 'You', handle: 'you', text: input.text.slice(0, 280), likes: 0, replies: 0 }; posts.unshift(post); return json(res, 201, post); }
    json(res, 404, { error: 'not found' });
  } catch (error) { json(res, 500, { error: error.message }); }
});
server.listen(port, '127.0.0.1', () => process.stdout.write(`qwik-twitter fixture listening on http://127.0.0.1:${port}\n`));
process.on('SIGTERM', () => server.close(() => process.exit(0))); process.on('SIGINT', () => server.close(() => process.exit(0)));
