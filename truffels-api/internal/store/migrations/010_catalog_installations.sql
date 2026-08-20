CREATE TABLE IF NOT EXISTS catalog_installations (
    id         TEXT PRIMARY KEY,
    catalog_id TEXT NOT NULL,
    params     TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
