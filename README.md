# MeshMonday

Simple web service for MeshCore Monday check-ins.

## Features
- MQTT ingestion for `meshcore/+/+/packets` (all IATA regions) with topic metadata extraction per packet
- MeshCore packet decoding (header/path/payload extraction)
- Monday check-in board with DiceBear avatars and animated tiles
- Leaderboard with:
  - Most check-ins (tracked from configurable date)
  - Longest Monday streak (with streak start date)
- SQLite storage with WAL mode and dedupe by packet hash

## Local macOS development (non-Docker)
1. `make setup`
2. Start an MQTT broker locally (`localhost:1883`) or point `.env.local` to your broker.
3. Run app once: `make run`
4. Run with hot reload: `make dev` (auto-installs `air` if needed)
5. Seed sample data (optional): `make dev-seed`
6. Replay fixtures (optional): `make replay-checkins`
7. Rebuild checkin packet links from stored raw packets (optional): `make restore-checkin-packets`

Open `http://127.0.0.1:8080`.

### Channel decryption keys
- Public channel key is built in: `8b3387e9c5cdea6ac9e5edbaa115cd72`.
- Deterministic hashtag channels: set `HASHTAG_CHANNELS` (comma-separated names, with or without `#`).
- Private channels: set `PRIVATE_CHANNEL_KEYS` as comma-separated 16-byte hex keys.

### Ingest safety limits
- `MQTT_MAX_PAYLOAD_BYTES` caps raw MQTT message size before processing (default `16384`).
- `INGEST_MAX_PACKET_HEX_CHARS` caps packet/envelope raw hex length accepted by ingest (default `8192`).
- `INGEST_MAX_OBSERVER_KEY_CHARS` caps observer key length from envelope/topic (default `64`).

### UI settings
- DiceBear avatar style: set `DICEBEAR_STYLE` to any DiceBear style slug (e.g. `rings`, `adventurer`, `pixel-art`, `bottts`).
- Auto-refresh polling interval: set `UI_POLL_SECONDS` (default `15`, set `0` to disable).

### IATA filtering
- Ingest subscribes to all IATAs via `meshcore/+/+/packets`.
- Display/query filtering is controlled by `IATA_FILTERS`:
  - `ALL` for all regions
  - comma-separated list such as `SEA,PDX,YVR`

## Key paths
- Server entrypoint: `cmd/server/main.go`
- MQTT replay utility: `cmd/replay/main.go`
- Seed utility: `cmd/devseed/main.go`
- SQLite + migrations: `internal/storage/sqlite.go`
- Decode logic: `internal/meshcore/decode.go`
- Monday page: `web/templates/index.html`
- Leaderboard page: `web/templates/leaderboard.html`

## Tests
- `make test`
- `make test-integration`

## Docker for production
1. Create `.env` with production values.
2. Run `docker compose up -d --build`

### Image automation
- GitHub Actions now builds Docker images automatically:
  - Pull requests to `main`: build-only validation (no push).
  - Pushes to `main` and version tags (`v*`): build and push to GHCR.
- Image builds are multi-arch for `linux/amd64` and `linux/arm64`.
- Published image path: `ghcr.io/agessaman/meshmonday`

### Public deployment recommendations
- Run behind TLS termination (reverse proxy or load balancer).
- Keep default server timeouts and header size limits in place.
- Avoid exposing internal errors to clients (API returns generic `internal_error` on failures).
- Use MQTT credentials with publish ACLs scoped to expected topics.

## Backups
Backup helper: `scripts/backup_sqlite.sh`.

## Maintenance
- Rebuild `checkin_packets` from `raw_packets` with current parsing/decryption rules:
  - `make restore-checkin-packets`
- This is useful after schema cleanup or if packet-link rows were lost.
- Raw packet retention runs inside the server process (`RAW_MONDAY_RETAIN_WEEKS`, `RETENTION_INTERVAL_MINUTES`). Checkins and linked rows are always kept; ambient mesh traffic is trimmed to recent Mondays in `TZ`.
- SQLite connections use a 30s `busy_timeout` on every pooled connection so MQTT inserts wait briefly instead of failing while retention deletes run.
- **`DELETE` does not shrink the `.db` file**: freed pages go on SQLite’s freelist inside the file; the OS sees the same size until you run `VACUUM` (or rebuild). Retention logs `freelist_mib` after each prune so you can see how much space is logically free but still inside the file. The server runs a WAL checkpoint after pruning to trim the `-wal` sidecar when possible.
- **`raw_packets` vs `packet_observations`**: the first stores each distinct packet payload (large hex blobs). The second stores which observer gateway reported each `(packet_hash, observer)` pair for Mesh Monday check-in observer badges—not duplicate payloads. Both tables shrink when `raw_packets` rows are deleted (FK cascade); the file still needs `VACUUM` to give disk space back.
- After a large prune, compact on disk: `make vacuum` (uses `SQLITE_PATH` from your env file). Run during a low-traffic window; `VACUUM` needs temporary disk headroom roughly the size of the database.

### Migrating check-ins to a new production database
Yes. Canonical rows live in `checkins`; those rows foreign-key to `raw_packets(packet_hash)`, and `checkin_packets` / `packet_observations` hang off the same hashes. Export everything needed in FK order:

1. Create the destination database with the **same app version** (run the server once against `SQLITE_PATH`, or apply the same schema), and **stop** the server before importing so nothing else writes the file.
2. From the old database: `./scripts/export_checkins_bundle.sh ./data/old.db > checkins_bundle.sql`  
   This pulls `raw_packets` rows referenced by check-ins, all `checkins`, `checkin_packets`, and matching `packet_observations` (for observer badges).
3. Import: `sqlite3 ./data/new.db < checkins_bundle.sql`
4. `leaderboard_snapshots` is not exported; the leaderboard API recomputes from `checkins` when loaded. If you added **custom tables** locally (not in this repo), copy those separately.

Example nightly cron:
`0 2 * * * cd /opt/meshmonday && ./scripts/backup_sqlite.sh ./data/meshmonday_prod.db ./backups`
