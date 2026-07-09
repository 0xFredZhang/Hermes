CREATE TABLE IF NOT EXISTS environment_secrets (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    environment_id INTEGER NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    kind           TEXT    NOT NULL,
    username_enc   TEXT    NOT NULL,
    password_enc   TEXT    NOT NULL,
    metadata_json  TEXT    NOT NULL DEFAULT '{}',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(environment_id, kind)
);
