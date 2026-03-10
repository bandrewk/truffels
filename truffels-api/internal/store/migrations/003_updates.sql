CREATE TABLE IF NOT EXISTS update_checks (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    service_id      TEXT NOT NULL,
    current_version TEXT NOT NULL DEFAULT '',
    latest_version  TEXT NOT NULL DEFAULT '',
    has_update      INTEGER NOT NULL DEFAULT 0,
    checked_at      TEXT NOT NULL DEFAULT (datetime('now')),
    error           TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (service_id) REFERENCES services(id)
);

CREATE TABLE IF NOT EXISTS update_log (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    service_id       TEXT NOT NULL,
    from_version     TEXT NOT NULL,
    to_version       TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending',
    started_at       TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at     TEXT,
    error            TEXT NOT NULL DEFAULT '',
    rollback_version TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (service_id) REFERENCES services(id)
);

CREATE INDEX IF NOT EXISTS idx_update_checks_service ON update_checks(service_id, checked_at);
CREATE INDEX IF NOT EXISTS idx_update_log_service ON update_log(service_id, started_at);
