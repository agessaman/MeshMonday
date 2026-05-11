#!/usr/bin/env bash
# Usage: ./scripts/export_checkins_bundle.sh /path/to/source.db > checkins_bundle.sql
# Imports check-in history into an empty DB that already has MeshMonday schema (same version).
set -euo pipefail

SRC="${1:?usage: $0 path/to/source.db > checkins_bundle.sql}"

# Dot-commands (.mode) only work on stdin or interactive input—not inside one "-quoted SQL string.
sqlite3 "$SRC" <<'SQL'
.mode insert raw_packets
SELECT rp.* FROM raw_packets rp
WHERE rp.packet_hash IN (
  SELECT packet_hash FROM checkins
  UNION
  SELECT packet_hash FROM checkin_packets
);
.mode insert checkins
SELECT * FROM checkins;
.mode insert checkin_packets
SELECT * FROM checkin_packets;
.mode insert packet_observations
SELECT po.* FROM packet_observations po
WHERE po.packet_hash IN (
  SELECT packet_hash FROM checkins
  UNION
  SELECT packet_hash FROM checkin_packets
);
SQL
