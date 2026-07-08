CREATE TABLE projects (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE blueprints (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id       INTEGER NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    name             TEXT NOT NULL,
    cloud_account_id INTEGER NOT NULL REFERENCES cloud_accounts(id) ON DELETE RESTRICT,
    params_json      TEXT NOT NULL,
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE environments (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    blueprint_id            INTEGER NOT NULL REFERENCES blueprints(id) ON DELETE RESTRICT,
    cloud_account_id        INTEGER NOT NULL REFERENCES cloud_accounts(id) ON DELETE RESTRICT,
    name                    TEXT NOT NULL,
    pulumi_stack            TEXT NOT NULL,
    region                  TEXT NOT NULL,
    blueprint_snapshot_json TEXT NOT NULL,
    status                  TEXT NOT NULL DEFAULT 'pending',
    outputs_json            TEXT NOT NULL DEFAULT '',
    created_at              TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE jobs (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    environment_id INTEGER NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    action         TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'queued',
    logs           TEXT NOT NULL DEFAULT '',
    summary_json   TEXT NOT NULL DEFAULT '',
    error          TEXT NOT NULL DEFAULT '',
    started_at     TIMESTAMP,
    finished_at    TIMESTAMP,
    created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
