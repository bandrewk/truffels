CREATE TABLE IF NOT EXISTS services (
    id         TEXT PRIMARY KEY,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS config_revisions (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    service_id        TEXT NOT NULL,
    timestamp         TEXT NOT NULL DEFAULT (datetime('now')),
    actor             TEXT NOT NULL DEFAULT 'admin',
    diff              TEXT NOT NULL,
    config_snapshot   TEXT NOT NULL,
    validation_result TEXT NOT NULL DEFAULT 'ok',
    FOREIGN KEY (service_id) REFERENCES services(id)
);

CREATE TABLE IF NOT EXISTS alerts (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    type        TEXT NOT NULL,
    severity    TEXT NOT NULL DEFAULT 'warning',
    service_id  TEXT,
    message     TEXT NOT NULL,
    first_seen  TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen   TEXT NOT NULL DEFAULT (datetime('now')),
    resolved    INTEGER NOT NULL DEFAULT 0,
    resolved_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_config_revisions_service ON config_revisions(service_id, timestamp);
CREATE INDEX IF NOT EXISTS idx_alerts_active ON alerts(resolved, last_seen);
