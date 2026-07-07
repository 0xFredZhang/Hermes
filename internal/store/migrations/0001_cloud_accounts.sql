CREATE TABLE cloud_accounts (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    name                  TEXT    NOT NULL,
    provider              TEXT    NOT NULL DEFAULT 'aws',
    default_region        TEXT    NOT NULL,
    access_key_id         TEXT    NOT NULL,
    secret_access_key_enc TEXT    NOT NULL,
    aws_account_id        TEXT    NOT NULL DEFAULT '',
    arn                   TEXT    NOT NULL DEFAULT '',
    created_at            TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
