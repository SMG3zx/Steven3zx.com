# ENG-85: Turso encryption at rest with the tursogo driver

Research for the Origin Admin map (ENG-80). The question is whether the admin database, `/data/admin.db` in the `admin_data` Docker volume, which holds admins and their sessions, can be encrypted at rest with a key from `edge/.env` that is never stored in the volume.

## Answer

**It works with the driver we already use, `turso.tech/database/tursogo` v0.8.3. No upgrade is needed, and there is no hard blocker.** The feature is still marked **experimental** upstream, though. The engine refuses to encrypt unless `experimental=encryption` is set, and Turso's own CLI flag says it is "not production ready so do not use it for critical data right now" ([Turso manual, CLI options](https://github.com/tursodatabase/turso/blob/main/docs/manual.md)). That is the only real risk, and it is acceptable for a small, rebuildable table of admins and sessions.

Each finding below is tagged by where it comes from:

- **[source]** comes from the tursogo v0.8.3 source in the module cache (`$GOMODCACHE/turso.tech/database/tursogo@v0.8.3`).
- **[docs]** comes from docs.turso.tech or the turso repo.
- **[tested]** was checked with a throwaway Go program against tursogo v0.8.3 on Windows. The program lived in a temp directory and never touched the `admin` container or its volume.

## 1. Engine support: cipher, granularity, status

- **It is supported for local files.** Encryption happens per page: "Each 4 KiB page is encrypted individually with a unique nonce" ([docs.turso.tech/tursodb/encryption](https://docs.turso.tech/tursodb/encryption)). A fresh random nonce is used on every page write, and the tag and nonce live in each page's reserved bytes. With AEGIS-256 that is 48 bytes per page: a 16-byte tag and a 32-byte nonce ([manual, Appendix A "Encryption"](https://github.com/tursodatabase/turso/blob/main/docs/manual.md)).
- **Ciphers:** `aes128gcm`, `aes256gcm`, `aegis128l`, `aegis128x2`, `aegis128x4`, `aegis256`, `aegis256x2`, `aegis256x4`. The docs recommend **`aegis256`** as the default [docs]. Keys are hex strings: 64 hex characters for 256-bit ciphers, 32 for 128-bit ones. Generate one with `openssl rand -hex 32` [docs].
- **It is not whole-file encryption.** The first 100 bytes of page 1 (the SQLite header) stay unencrypted and are used as authenticated data. The first 16 bytes are replaced by a Turso header: `"Turso"`, a version byte, and a cipher-ID byte [manual]. **[tested]** An encrypted file starts with `Turso\x00\x03…`, where cipher ID 3 is AEGIS-256. The cipher can be read from the file, but the key cannot.
- **Key storage:** "The key is not stored anywhere". Every connection has to supply it [manual]. That fits the requirement to keep the key off the volume.
- **Status:** experimental. The engine gates it behind an experimental-features flag; see "Answer" above. The changelog shows active fixes through 0.8.0 (2026-09-28), for example "Fix encrypted fresh DB page-size retargeting". It has no entry declaring encryption stable ([CHANGELOG.md](https://github.com/tursodatabase/turso/blob/main/CHANGELOG.md)).
- **Encrypted files only open with Turso.** Plain `sqlite3` tools cannot read them; with no key the open fails with "file is not a database" [manual, tested].

## 2. Passing the key through tursogo / `database/sql`

v0.8.3 exposes the key as **DSN query parameters** [source]. `driver_db.go` documents and implements this:

```go
// parseDSN supports format: <path>[?experimental=<string>&async=0|1&vfs=<string>&encryption_cipher=<string>&encryption_hexkey=<string>&_busy_timeout=<int>]
```

These parameters map to `TursoDatabaseConfig.Encryption{Cipher, Hexkey}`. The source comment on that struct says "as encryption is experimental - ExperimentalFeatures must have "encryption" in the list" (`bindings_db.go`). The driver's own test, `TestEncryption` in `driver_db_test.go`, uses exactly this form: `?experimental=encryption&encryption_cipher=aegis256&encryption_hexkey=<hex>`.

Behavior of each setting [tested]:

| DSN | Result |
|---|---|
| correct cipher + key + `experimental=encryption` | opens, reads and writes |
| wrong key | `Decryption failed for page=1` (on first query; `sql.Open` itself succeeds) |
| no key | `not a database: file is not a database` |
| key without `experimental=encryption` | `encryption is experimental and must be explicitly enabled through experimental features list` |
| key without cipher | `API misuse: either both encryption cipher and key must be set or no` |
| different cipher (`aes256gcm`) with the right key | `Decryption failed for page=1` (cipher must match what the file was created with) |

You can also run `PRAGMA cipher = 'aegis256'; PRAGMA hexkey = '…';` on a fresh, empty file [manual, tested]. **Do not run them on an existing plaintext database.** In our test that made the connection fail to read (`Decryption failed`) and did not convert anything; the file was still readable without a key afterwards. Use the DSN form only.

`sql.Open` succeeds even with a bad key, so `openStore` should run a query (the schema `Exec` already does) to fail fast.

### Exact code

`edge/compose.yaml`, admin service. The key goes in `edge/.env`, which is not done here:

```yaml
    environment:
      HOST_AGENT_TOKEN: ${HOST_AGENT_TOKEN}
      ADMIN_DB_KEY: ${ADMIN_DB_KEY}        # 64 hex chars: openssl rand -hex 32
```

`portfolio/admin/server/store.go`:

```go
// openStore opens the admin database encrypted with AEGIS-256. hexKey is 64
// hex characters and is never written to disk.
func openStore(path, hexKey string) (*store, error) {
	if len(hexKey) != 64 {
		return nil, errors.New("ADMIN_DB_KEY must be 64 hex characters")
	}
	if _, err := hex.DecodeString(hexKey); err != nil {
		return nil, errors.New("ADMIN_DB_KEY must be hex")
	}
	dsn := path + "?experimental=encryption&encryption_cipher=aegis256&encryption_hexkey=" + hexKey
	db, err := sql.Open("turso", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil { // fails here on a wrong key
		db.Close()
		return nil, err
	}
	return &store{db: db}, nil
}
```

`main.go`: `openStore(env("ADMIN_DB", "data/admin.db"), os.Getenv("ADMIN_DB_KEY"))`. Don't log the DSN, because it contains the key.

## 3. WAL and temp files

- **The WAL is encrypted.** A design goal is "All pages in db and WAL file to be encrypted on disk" [manual]. **[tested]** After writing 500 rows without a checkpoint, neither `x.db` nor `x.db-wal` (28 KB) contained the plaintext. The driver test checks the main file after `PRAGMA wal_checkpoint(TRUNCATE)` [source].
- **Temp files: not covered by encryption.** The sorter spills to a `TempFile` when its in-memory buffer fills, and the spilled chunks are raw record payloads with no encryption step ([core/vdbe/sorter.rs](https://github.com/tursodatabase/turso/blob/main/core/vdbe/sorter.rs)). Neither the docs nor the manual say anything about temp files. For us this is low risk:
  - our queries are point lookups on tiny tables, so nothing spills;
  - `edge/compose.yaml` already mounts `/tmp` as tmpfs, so a spill would go to RAM, not the volume (this assumes `TempFile` uses the OS temp dir, which was not verified);
  - `PRAGMA temp_store = MEMORY` is accepted on an encrypted connection and reads back `2` [tested]. Adding it to `schema` is a cheap belt-and-braces step.

## 4. Converting the existing unencrypted database

Things that **do not** work [tested]:
- `VACUUM INTO 'file:new.db?cipher=…&hexkey=…'` fails with `I/O error (open): invalid filename`.
- `PRAGMA cipher`/`hexkey` on an existing plaintext file does not convert it.
- The docs list "encrypting existing databases" as **Coming Soon** [docs].

What **works** [tested]: open the new file encrypted, `ATTACH` the old plaintext file, and copy. `ATTACH` is a separate experimental feature, so the DSN needs `experimental=encryption,attach`. Without it you get `ATTACH is an experimental feature`. The new file came out with no plaintext, and the old file was unchanged.

One-off migration. Run it with the server stopped, as a temporary subcommand or a small Go program in the image:

```go
dsn := "/data/admin.enc.db?experimental=encryption,attach&encryption_cipher=aegis256&encryption_hexkey=" + key
db, _ := sql.Open("turso", dsn)
db.SetMaxOpenConns(1)
db.Exec(schema)                                   // same schema const as store.go
db.Exec(`ATTACH '/data/admin.db' AS old`)
db.Exec(`INSERT INTO users SELECT * FROM old.users`)
// sessions can be skipped: every admin just signs in again
db.Exec(`DETACH old`)
db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
db.Close()
```

Then:

1. Rename `admin.db` to `admin.plain.db` and `admin.enc.db` to `admin.db`.
2. Start the server with `ADMIN_DB_KEY` set and sign in.
3. Delete `admin.plain.db` and its `-wal`.

Alternatively, since the database only holds a few admins, start fresh with an encrypted file and re-run `useradd`. That needs no migration code.

## 5. Performance, key rotation, backups

- **Performance:** For AEGIS-256 the docs measure about 6% read overhead and 14% write overhead, and 1–3% on mixed workloads [docs]. **[tested]** 2,000 single-row inserts and 2,000 point reads on a session-shaped table showed no measurable difference: writes took 1.71 s plain vs 1.71 s encrypted, reads 53 ms vs 48 ms. Write time is dominated by fsync. This is negligible for Origin Admin.
- **Key rotation:** there is no rekey; it is "Coming Soon" [docs], and "rekeying would require writing the entire database" [manual]. **[tested]** You can rotate with the same ATTACH copy as the migration. Open a new file with the new key and attach the old one with its key in a URI:
  `ATTACH 'file:/data/admin.db?cipher=aegis256&hexkey=<old>' AS old`, then copy and swap files. The result was readable only with the new key.
- **Lost key = lost data** [docs]. Keep `ADMIN_DB_KEY` somewhere besides `edge/.env`, for example a password manager.
- **Backups — pitfall:** `VACUUM INTO 'backup.db'` from an encrypted connection writes a **plaintext** copy [tested]. Upstream treats this as intended: "When an encrypted db is opened and does vacuum into, we should get an unencrypted db as out" ([turso#5851](https://github.com/tursodatabase/turso/pull/5851)). Two options:
  - back up by copying `admin.db` and `admin.db-wal` (or the volume) while the server is stopped or after a checkpoint, which keeps the copy encrypted;
  - or treat any `VACUUM INTO` output as sensitive.

## 6. What changes for `useradd` / `passwd`

They call the same `openStore` from `main.go`, and the README runs them as `docker compose run --rm -it admin useradd NAME`. `docker compose run` uses the service's `environment:` block, so once `ADMIN_DB_KEY: ${ADMIN_DB_KEY}` is in `edge/compose.yaml` they get the key automatically. The only code change is passing the key into `openStore`. If someone runs them without the key, they fail with a clear `ADMIN_DB_KEY must be 64 hex characters` error instead of touching the file. If someone runs the binary outside Compose, they have to export `ADMIN_DB_KEY` first.

## Blockers

None. The caveats are:

1. The feature is experimental upstream, and the DSN must carry `experimental=encryption`.
2. There is no in-place conversion or rekey; use the ATTACH copy.
3. `VACUUM INTO` backups come out in plaintext.
4. Temp spill files are not encrypted; this is mitigated by the tmpfs `/tmp`.
