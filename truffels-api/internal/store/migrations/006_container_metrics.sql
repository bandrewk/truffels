-- Per-container resource snapshots (CPU, memory, network I/O, block I/O)
CREATE TABLE IF NOT EXISTS container_snapshots (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp        TEXT NOT NULL DEFAULT (datetime('now')),
    container        TEXT NOT NULL,
    cpu_percent      REAL NOT NULL,
    mem_usage_mb     REAL NOT NULL,
    mem_limit_mb     REAL NOT NULL,
    net_rx_bytes     INTEGER NOT NULL,
    net_tx_bytes     INTEGER NOT NULL,
    block_read_bytes  INTEGER NOT NULL DEFAULT 0,
    block_write_bytes INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_container_snapshots_ts ON container_snapshots(timestamp);
CREATE INDEX IF NOT EXISTS idx_container_snapshots_name ON container_snapshots(container, timestamp);
