CREATE TABLE cloud_catalog_cache (
    cloud_account_id INTEGER NOT NULL REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    kind             TEXT    NOT NULL,
    region           TEXT    NOT NULL DEFAULT '',
    lookup_key       TEXT    NOT NULL DEFAULT '',
    payload_json     TEXT    NOT NULL,
    fetched_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (cloud_account_id, kind, region, lookup_key)
);
