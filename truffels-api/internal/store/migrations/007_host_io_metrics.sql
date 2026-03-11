-- Add host network and disk I/O columns to metric_snapshots.
-- Add block I/O columns to container_snapshots.
-- Uses CREATE TABLE to add column if missing (ignores error if column exists via Go handler).

-- We rely on the Go migrate() wrapper to handle "duplicate column" errors gracefully.
-- These ALTERs will fail silently on fresh DBs (columns already in CREATE TABLE).
ALTER TABLE metric_snapshots ADD COLUMN net_rx_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE metric_snapshots ADD COLUMN net_tx_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE metric_snapshots ADD COLUMN disk_read_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE metric_snapshots ADD COLUMN disk_write_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE metric_snapshots ADD COLUMN disk_io_percent REAL NOT NULL DEFAULT 0;
ALTER TABLE container_snapshots ADD COLUMN block_read_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE container_snapshots ADD COLUMN block_write_bytes INTEGER NOT NULL DEFAULT 0;
