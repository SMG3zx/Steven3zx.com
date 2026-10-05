import { defineConfig } from 'vite';
import { readFile, readdir } from 'node:fs/promises';
import path from 'node:path';

// Local development bridge only. Private snapshots are never copied into public/ or the build.
export default defineConfig({
  plugins: [
    {
      name: 'local-adam-snapshots',
      configureServer(server) {
        server.middlewares.use('/api/adam-snapshot', async (req, res) => {
          if (req.method !== 'GET') {
            res.statusCode = 405;
            res.end();
            return;
          }
          try {
            const root = process.env.EVE_ADAM_ROOT || path.resolve(process.cwd(), 'data/adam');
            const files = (await readdir(root)).filter((file) =>
              /^ADAM_JSON_SCHEMA_.+\.json$/i.test(file),
            );
            if (!files.length) throw new Error('No ADAM snapshot files found');
            const snapshots = await Promise.all(
              files.map((file) => readFile(path.join(root, file), 'utf8').then(JSON.parse)),
            );
            const locations = [
              ...new Set(
                snapshots.flatMap((snapshot) =>
                  Object.entries(snapshot.data || {})
                    .map(([key, rack]) => rack?.LOC || key)
                    .filter((loc) => /^\d+[A-Z]\d{3}$/.test(loc)),
                ),
              ),
            ];
            if (!locations.length) throw new Error('No configured locations');
            res.setHeader('Content-Type', 'application/json');
            res.setHeader('Cache-Control', 'no-store');
            res.end(JSON.stringify({ source: 'historical', snapshots, locations }));
          } catch {
            res.statusCode = 503;
            res.end('Local ADAM snapshots unavailable. Configure EVE_ADAM_ROOT or VITE_WORLD_URL.');
          }
        });
      },
    },
  ],
});
