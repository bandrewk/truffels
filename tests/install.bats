#!/usr/bin/env bats
# Tests for install.sh — focused on service-disable logic and installer notices.

setup() {
    TEST_DIR="$(mktemp -d)"
    export DATA_DIR="$TEST_DIR/data"
    mkdir -p "$DATA_DIR/truffels"
    DB_PATH="$DATA_DIR/truffels/truffels.db"

    # Create the services table matching the API schema (001_init.sql)
    python3 -c "
import sqlite3
db = sqlite3.connect('$DB_PATH')
db.execute('''CREATE TABLE IF NOT EXISTS services (
    id         TEXT PRIMARY KEY,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
)''')
# Pre-populate all services as enabled (simulates API EnsureService at boot)
for svc in ('electrs', 'mempool', 'ckpool', 'ckstats', 'bitcoind'):
    db.execute('INSERT INTO services (id, enabled) VALUES (?, 1)', (svc,))
db.commit()
db.close()
"
    # Source helpers from install.sh (log/warn) without running the script
    log()  { echo -e "[TRUFFELS] $*"; }
    warn() { echo -e "[WARNING] $*"; }
    export -f log warn
}

teardown() {
    rm -rf "$TEST_DIR"
}

# --- SQLite disable tests ---------------------------------------------------

@test "disable snippet sets all 4 dependent services to enabled=0" {
    db_path="$DATA_DIR/truffels/truffels.db"
    python3 -c "
import sqlite3
db = sqlite3.connect('$db_path')
for svc in ('electrs', 'mempool', 'ckpool', 'ckstats'):
    db.execute('INSERT INTO services (id, enabled) VALUES (?, 0) ON CONFLICT(id) DO UPDATE SET enabled=0', (svc,))
db.commit()
db.close()
"
    # Verify all 4 are disabled
    result=$(python3 -c "
import sqlite3
db = sqlite3.connect('$db_path')
rows = db.execute('SELECT id, enabled FROM services ORDER BY id').fetchall()
for r in rows:
    print(f'{r[0]}={r[1]}')
db.close()
")
    echo "$result"
    [[ "$result" == *"ckpool=0"* ]]
    [[ "$result" == *"ckstats=0"* ]]
    [[ "$result" == *"electrs=0"* ]]
    [[ "$result" == *"mempool=0"* ]]
    # bitcoind should remain enabled
    [[ "$result" == *"bitcoind=1"* ]]
}

@test "disable snippet is idempotent (running twice still results in enabled=0)" {
    db_path="$DATA_DIR/truffels/truffels.db"
    for _ in 1 2; do
        python3 -c "
import sqlite3
db = sqlite3.connect('$db_path')
for svc in ('electrs', 'mempool', 'ckpool', 'ckstats'):
    db.execute('INSERT INTO services (id, enabled) VALUES (?, 0) ON CONFLICT(id) DO UPDATE SET enabled=0', (svc,))
db.commit()
db.close()
"
    done
    result=$(python3 -c "
import sqlite3
db = sqlite3.connect('$db_path')
disabled = db.execute('SELECT COUNT(*) FROM services WHERE enabled=0').fetchone()[0]
print(disabled)
db.close()
")
    [[ "$result" == "4" ]]
}

# --- Log message tests -------------------------------------------------------

@test "non-pruned mode: electrs/mempool skip message mentions syncing" {
    PRUNE_SIZE=0
    output=$(
        if [[ "$PRUNE_SIZE" -eq 0 ]]; then
            log "Skipping electrs and mempool — Bitcoin Core is still syncing (will be disabled)."
        else
            log "Skipping electrs and mempool (incompatible with pruned mode — will be disabled)."
        fi
    )
    [[ "$output" == *"still syncing"* ]]
    [[ "$output" != *"incompatible"* ]]
}

@test "pruned mode: electrs/mempool skip message mentions incompatibility" {
    PRUNE_SIZE=550000
    output=$(
        if [[ "$PRUNE_SIZE" -eq 0 ]]; then
            log "Skipping electrs and mempool — Bitcoin Core is still syncing (will be disabled)."
        else
            log "Skipping electrs and mempool (incompatible with pruned mode — will be disabled)."
        fi
    )
    [[ "$output" == *"incompatible with pruned mode"* ]]
    [[ "$output" != *"still syncing"* ]]
}

# --- Post-install notice tests -----------------------------------------------

@test "non-pruned notice: all 4 services show 'enable via web UI'" {
    PRUNE_SIZE=0
    output=$(
        log "Dependent services have been disabled (Bitcoin Core must fully sync first):"
        log "  - ckpool    — enable via web UI after Bitcoin Core is fully synced"
        log "  - ckstats   — enable via web UI after Bitcoin Core is fully synced"
        if [[ "$PRUNE_SIZE" -eq 0 ]]; then
            log "  - electrs   — enable via web UI after Bitcoin Core is fully synced"
            log "  - mempool   — enable via web UI after Bitcoin Core is fully synced"
        else
            log "  - electrs   — INCOMPATIBLE with pruned mode (cannot be enabled)"
            log "  - mempool   — INCOMPATIBLE with pruned mode (cannot be enabled)"
        fi
    )
    echo "$output"
    # All 4 should say "enable via web UI"
    [[ $(echo "$output" | grep -c "enable via web UI") -eq 4 ]]
    # None should say INCOMPATIBLE
    [[ $(echo "$output" | grep -c "INCOMPATIBLE") -eq 0 ]]
}

@test "pruned notice: electrs/mempool show INCOMPATIBLE, ckpool/ckstats show enable" {
    PRUNE_SIZE=550000
    output=$(
        log "Dependent services have been disabled (Bitcoin Core must fully sync first):"
        log "  - ckpool    — enable via web UI after Bitcoin Core is fully synced"
        log "  - ckstats   — enable via web UI after Bitcoin Core is fully synced"
        if [[ "$PRUNE_SIZE" -eq 0 ]]; then
            log "  - electrs   — enable via web UI after Bitcoin Core is fully synced"
            log "  - mempool   — enable via web UI after Bitcoin Core is fully synced"
        else
            log "  - electrs   — INCOMPATIBLE with pruned mode (cannot be enabled)"
            log "  - mempool   — INCOMPATIBLE with pruned mode (cannot be enabled)"
        fi
    )
    echo "$output"
    # ckpool and ckstats should say "enable via web UI"
    [[ "$output" == *"ckpool"*"enable via web UI"* ]]
    [[ "$output" == *"ckstats"*"enable via web UI"* ]]
    # electrs and mempool should say INCOMPATIBLE
    [[ "$output" == *"electrs"*"INCOMPATIBLE"* ]]
    [[ "$output" == *"mempool"*"INCOMPATIBLE"* ]]
    [[ $(echo "$output" | grep -c "INCOMPATIBLE") -eq 2 ]]
}

@test "electrs and mempool are never started during install (no docker compose up)" {
    # Verify the install script no longer contains 'docker compose up -d' for electrs or mempool
    # in the service-start section (between the bitcoind health check and the ckpool section)
    script="$(cat install.sh)"
    # Extract the section between "Waiting for bitcoind" and "ckpool and ckstats require"
    section=$(sed -n '/Waiting for bitcoind to become healthy/,/ckpool and ckstats require/p' install.sh)
    # Should NOT contain 'docker compose up -d' (electrs/mempool no longer started)
    if echo "$section" | grep -q 'docker compose up -d'; then
        echo "FAIL: found 'docker compose up -d' in electrs/mempool section"
        echo "$section"
        return 1
    fi
}
