CREATE TABLE IF NOT EXISTS metric_snapshots (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp    TEXT NOT NULL DEFAULT (datetime('now')),
    cpu_percent  REAL NOT NULL,
    mem_percent  REAL NOT NULL,
    temp_c       REAL NOT NULL,
    disk_percent REAL NOT NULL,
    fan_rpm      INTEGER NOT NULL DEFAULT 0,
    fan_percent  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_metric_snapshots_ts ON metric_snapshots(timestamp);

CREATE TABLE IF NOT EXISTS service_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp   TEXT NOT NULL DEFAULT (datetime('now')),
    service_id  TEXT NOT NULL,
    container   TEXT NOT NULL DEFAULT '',
    event_type  TEXT NOT NULL,
    from_state  TEXT NOT NULL DEFAULT '',
    to_state    TEXT NOT NULL DEFAULT '',
    message     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_service_events_ts ON service_events(service_id, timestamp);
