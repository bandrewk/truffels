-- Data-dir size snapshots (added in v0.3.1-dev.21).
--
-- Primary use: catch mempool's rbfcache.json runaway days before it OOMs
-- the backend. Cache path is /srv/truffels/data/mempool/cache. The collector
-- polls the agent every ~60s and persists one row per tick.
--
-- FUTURE_WORK.md noted this metric was already planned after the dev.20
-- post-mortem, and this migration ships it.
CREATE TABLE IF NOT EXISTS dir_size_snapshots (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp   TEXT NOT NULL DEFAULT (datetime('now')),
    path        TEXT NOT NULL,
    size_bytes  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_dir_size_snapshots_ts   ON dir_size_snapshots(timestamp);
CREATE INDEX IF NOT EXISTS idx_dir_size_snapshots_path ON dir_size_snapshots(path, timestamp);
