# Installing MeshMonday

This guide covers running MeshMonday in production using the prebuilt
multi-arch Docker image published to GHCR, deployed with `docker compose`,
and fronted by either nginx or Apache as a TLS-terminating reverse proxy.

For local non-Docker development, see [README.md](README.md) instead.

- Repository: <https://github.com/agessaman/MeshMonday>
- Image: `ghcr.io/agessaman/meshmonday`
- Architectures: `linux/amd64`, `linux/arm64`
- Available tags:
  - `latest` — most recent push to `main`
  - `main` — same as latest
  - `vX.Y.Z` — release tags
  - `sha-<commit>` — pinned to a specific commit

Pinning to a release tag (e.g. `v1.0.0`) is recommended for production.

## Prerequisites

- A Linux host with Docker Engine 24+ and the Compose plugin
  (`docker compose version` works)
- Outbound network access to your MQTT broker
- An MQTT user with subscribe permission on `meshcore/+/+/packets`
- A DNS name pointing at the host (for TLS)
- nginx or Apache installed on the host (or a separate proxy host)

## 1. Create the deployment directory

```bash
sudo mkdir -p /opt/meshmonday/{data,backups}
sudo chown -R "$USER":"$USER" /opt/meshmonday
cd /opt/meshmonday
```

The `data/` directory holds the SQLite database (with WAL files) and
`backups/` is where `scripts/backup_sqlite.sh` writes snapshots.

## 2. Create `docker-compose.yml`

This compose file uses the prebuilt image (no local build context), keeps
the app bound to loopback on the host, and persists SQLite data to disk.

```yaml
services:
  app:
    image: ghcr.io/agessaman/meshmonday:latest
    container_name: meshmonday
    env_file:
      - .env
    ports:
      # Bind only to localhost; the reverse proxy will forward to it.
      - "127.0.0.1:8080:8080"
    volumes:
      - ./data:/app/data
      - ./backups:/app/backups
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/healthz"]
      interval: 30s
      timeout: 5s
      retries: 5
      start_period: 20s
```

Notes:

- Replace `:latest` with a pinned tag such as `:v1.0.0` for production.
- The host port binding `127.0.0.1:8080:8080` ensures the app is only
  reachable through the reverse proxy. Drop the `127.0.0.1:` prefix only
  if you intend to expose port 8080 publicly (not recommended).

## 3. Create `.env`

Create `/opt/meshmonday/.env` with at minimum the following. Adjust values
for your broker, mesh, and timezone.

```env
APP_ENV=production

# IMPORTANT: bind to all interfaces inside the container so the host
# port mapping works. The default (127.0.0.1:8080) only listens on the
# container's loopback and will appear unreachable from the host.
HTTP_ADDR=0.0.0.0:8080

MESH_NAME=CascadiaMesh
TZ=America/Los_Angeles

# SQLite path inside the container (matches the ./data volume mount)
SQLITE_PATH=/app/data/meshmonday_prod.db

# MQTT (production REQUIRES username and password)
MQTT_BROKER_URL=wss://mqtt.example.org:443/mqtt
MQTT_TOPIC_TEMPLATE=meshcore/+/+/packets
MQTT_CLIENT_ID=meshmonday-prod
MQTT_USERNAME=replace-me
MQTT_PASSWORD=replace-me

# Display and tracking
DICEBEAR_STYLE=fun-emoji
UI_POLL_SECONDS=15
TRACK_FROM_DATE=2026-01-05
IATA_FILTERS=ALL

# Channels (optional)
HASHTAG_CHANNELS=#meshmonday
PRIVATE_CHANNEL_KEYS=
```

Lock down the file:

```bash
chmod 600 .env
```

See [README.md](README.md) for the full list of supported variables
(channel keys, ingest safety limits, IATA filtering, etc.).

## 4. Pull and start

```bash
docker compose pull
docker compose up -d
docker compose logs -f app
```

Verify health from the host:

```bash
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
```

You should now choose **either** the nginx **or** the Apache section
below — not both.

## 5a. nginx reverse proxy (recommended)

Install nginx if it isn't already, then create
`/etc/nginx/sites-available/meshmonday.conf` (Debian/Ubuntu layout) or
`/etc/nginx/conf.d/meshmonday.conf` (RHEL layout):

```nginx
# Redirect plain HTTP to HTTPS.
server {
    listen 80;
    listen [::]:80;
    server_name meshmonday.example.org;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    http2 on;
    server_name meshmonday.example.org;

    # TLS — adjust paths to your certificates (e.g. certbot output).
    ssl_certificate     /etc/letsencrypt/live/meshmonday.example.org/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/meshmonday.example.org/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         HIGH:!aNULL:!MD5;
    ssl_prefer_server_ciphers on;

    # Reasonable defaults; the app already sets its own security headers.
    client_max_body_size 1m;

    # Static assets are served by the app under /static/.
    location / {
        proxy_pass         http://127.0.0.1:8080;
        proxy_http_version 1.1;

        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host  $host;

        proxy_connect_timeout 5s;
        proxy_read_timeout    30s;
        proxy_send_timeout    30s;
    }

    # Optional: expose health endpoints only to localhost / monitoring.
    location = /healthz { proxy_pass http://127.0.0.1:8080/healthz; }
    location = /readyz  { proxy_pass http://127.0.0.1:8080/readyz; }
}
```

Enable and reload:

```bash
# Debian/Ubuntu:
sudo ln -s /etc/nginx/sites-available/meshmonday.conf /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

For TLS certificates, [certbot](https://certbot.eff.org/) with the nginx
plugin is the simplest option:

```bash
sudo certbot --nginx -d meshmonday.example.org
```

## 5b. Apache reverse proxy

Enable the modules required for HTTPS reverse proxying:

```bash
# Debian/Ubuntu
sudo a2enmod ssl proxy proxy_http headers rewrite
# RHEL/CentOS/Alma — these are typically loaded by default
```

Create `/etc/apache2/sites-available/meshmonday.conf` (Debian/Ubuntu) or
`/etc/httpd/conf.d/meshmonday.conf` (RHEL):

```apache
<VirtualHost *:80>
    ServerName meshmonday.example.org
    Redirect permanent / https://meshmonday.example.org/
</VirtualHost>

<VirtualHost *:443>
    ServerName meshmonday.example.org

    SSLEngine on
    SSLCertificateFile      /etc/letsencrypt/live/meshmonday.example.org/fullchain.pem
    SSLCertificateKeyFile   /etc/letsencrypt/live/meshmonday.example.org/privkey.pem
    SSLProtocol             all -SSLv3 -TLSv1 -TLSv1.1
    SSLHonorCipherOrder     on

    ProxyRequests     Off
    ProxyPreserveHost On

    # Forwarded headers so the app sees the real client and scheme.
    RequestHeader set X-Forwarded-Proto "https"
    RequestHeader set X-Forwarded-Host  "%{HTTP_HOST}s"

    ProxyPass        / http://127.0.0.1:8080/
    ProxyPassReverse / http://127.0.0.1:8080/

    ProxyTimeout 30

    ErrorLog  ${APACHE_LOG_DIR}/meshmonday-error.log
    CustomLog ${APACHE_LOG_DIR}/meshmonday-access.log combined
</VirtualHost>
```

Enable and reload:

```bash
# Debian/Ubuntu
sudo a2ensite meshmonday.conf
sudo apachectl configtest
sudo systemctl reload apache2

# RHEL/CentOS/Alma
sudo apachectl configtest
sudo systemctl reload httpd
```

For TLS certificates with certbot's Apache plugin:

```bash
sudo certbot --apache -d meshmonday.example.org
```

## 6. Verify end-to-end

From any client:

```bash
curl -fsS https://meshmonday.example.org/healthz
```

Then open `https://meshmonday.example.org/` in a browser — the Monday
check-in board should load, and `/leaderboard` should be reachable.

## Operations

### Upgrades

```bash
cd /opt/meshmonday
docker compose pull
docker compose up -d
docker image prune -f
```

Pin to a release tag (e.g. `ghcr.io/agessaman/meshmonday:v1.0.0`) to
avoid surprise upgrades when re-pulling `latest`.

### Backups

The repository ships with `scripts/backup_sqlite.sh`. From the host,
back up by copying directly out of the bind mount:

```bash
cp /opt/meshmonday/data/meshmonday_prod.db \
   /opt/meshmonday/backups/meshmonday-$(date +%Y%m%d-%H%M%S).db
```

Or download the script and run it on a schedule:

```cron
0 2 * * * cd /opt/meshmonday && \
  /opt/meshmonday/backup_sqlite.sh ./data/meshmonday_prod.db ./backups
```

### Logs

```bash
docker compose logs -f app           # follow
docker compose logs --since=1h app   # last hour
```

### Tearing down

```bash
docker compose down            # stop and remove the container
docker compose down --volumes  # also remove named volumes (none here by default)
```

The `./data` and `./backups` bind mounts persist on the host regardless.

## Troubleshooting

- **`curl: (7) Failed to connect to 127.0.0.1 port 8080`** — `HTTP_ADDR`
  is probably still the default `127.0.0.1:8080`. Set it to
  `0.0.0.0:8080` in `.env` and run `docker compose up -d`.
- **App exits at startup with `production requires MQTT_USERNAME and
  MQTT_PASSWORD`** — set both in `.env`; they are mandatory when
  `APP_ENV=production`.
- **502 Bad Gateway from the proxy** — confirm the container is healthy
  (`docker compose ps`) and reachable on `127.0.0.1:8080` from the host.
- **No check-ins appearing** — verify `MQTT_BROKER_URL`,
  `MQTT_TOPIC_TEMPLATE`, credentials, and that any `IATA_FILTERS` /
  `HASHTAG_CHANNELS` settings are not filtering everything out. Check
  `docker compose logs app` for MQTT connection errors.
