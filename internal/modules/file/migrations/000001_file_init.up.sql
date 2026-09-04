-- Schema of the file module (plan Section 5.6 — the FileProvider abstraction).
--
-- Ownership: this table belongs ONLY to the file module. uploaded_by is a user
-- or an API key id but it is NOT A FOREIGN KEY (Principle 2.2 — the
-- cross-module FK ban): the identity is owned by the auth module.
--
-- Time: every stamp is timestamptz (UTC).

-- file_uploads IS THE LEDGER OF THE UPLOADED FILES: it holds where the file
-- sits in the store, its type as detected FROM ITS CONTENT, its size, its
-- digest and who uploaded it.
--
-- # storage_key DOES NOT COME FROM THE CLIENT
--
-- The key is PRODUCED by the provider (the id plus the extension derived from
-- the detected type). The file name the client reports sits in a separate
-- column (original_name) and only for DISPLAY; it enters no path expression.
-- Path traversal ("../" and every encoding of it) is thereby made impossible
-- STRUCTURALLY, not by being "sanitized": sanitizing would have meant taking
-- the decision again at every new encoding trick.
--
-- # storage_key IS UNIQUE
--
-- Two records cannot point at the same file. The constraint protects two
-- things at once: deletion (the flow that removes the record and deletes the
-- file) cannot carry off another record's file, and the SERVING path can reach
-- the record from the key with a single row — the served Content-Type is
-- written from that row, so the question "which row" must have exactly one
-- answer.
--
-- # content_type COMES FROM THE CONTENT
--
-- The column carries NOT the client's Content-Type header but the type
-- net/http.DetectContentType detected from the first 512 bytes. The type the
-- client reports is a CLAIM: an HTML file sent as "image/png" passes an allow
-- list that trusts it and, once served, runs in the browser.
--
-- The allow list IS NOT WRITTEN as a CHECK constraint: the accepted types come
-- from the configuration (FILE_ALLOWED_TYPES) and vary from installation to
-- installation; pinning the list into the schema would have made writing a
-- migration mandatory for every settings change. What is written into the
-- ledger is a VALIDATED upload — the one doing the validating is the service
-- layer.
--
-- # THERE IS NO SOFT DELETE
--
-- Unlike in the other modules there is no deleted_at column, and the reason
-- lies in the deletion itself: when an upload is deleted THE FILE is deleted
-- from the store too. A soft-deleted row would keep a record whose file is
-- long gone in the list ("it is there but it does not open") and would go on
-- OCCUPYING the unique key.
CREATE TABLE IF NOT EXISTS file_uploads (
    id            TEXT        PRIMARY KEY,
    -- storage_key is the storage key produced by the provider; it DOES NOT
    -- COME from the client.
    storage_key   TEXT        NOT NULL,
    -- provider_id is the id of the provider that wrote the file. It is stored
    -- because an installation can change its provider and the old records can
    -- only be read by the provider that wrote them.
    provider_id   TEXT        NOT NULL,
    -- content_type is the type detected FROM THE CONTENT; when serving, the
    -- Content-Type header is written from this column.
    content_type  TEXT        NOT NULL,
    size          BIGINT      NOT NULL,
    -- checksum is the SHA-256 digest of the content (lowercase hexadecimal);
    -- it is for diagnostics.
    checksum      TEXT        NOT NULL,
    -- original_name is the name the client reported and it is ONLY for
    -- display. It may be empty: some clients send no name and that is not an
    -- error.
    original_name TEXT        NOT NULL DEFAULT '',
    -- url is the file's reachable address; on the local provider it is
    -- relative to the root.
    url           TEXT        NOT NULL,
    -- uploaded_by is the id of the uploading caller. THERE IS NO FK
    -- (Principle 2.2).
    uploaded_by   TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT file_uploads_storage_key_not_empty  CHECK (storage_key <> ''),
    CONSTRAINT file_uploads_provider_id_not_empty  CHECK (provider_id <> ''),
    CONSTRAINT file_uploads_content_type_not_empty CHECK (content_type <> ''),
    CONSTRAINT file_uploads_url_not_empty          CHECK (url <> ''),
    -- A zero-byte upload is always a failure: the type cannot be detected from
    -- the content either, and there is nothing to serve.
    CONSTRAINT file_uploads_size_positive          CHECK (size > 0)
);

-- Two records cannot point at the same storage key; this is the constraint
-- that lets the serving path reach the record from the key with a single row.
CREATE UNIQUE INDEX IF NOT EXISTS file_uploads_storage_key_uniq
    ON file_uploads (storage_key);

-- The admin list is paginated from the newest to the oldest; the index serves
-- that ordering.
CREATE INDEX IF NOT EXISTS file_uploads_recent_idx
    ON file_uploads (created_at DESC, id DESC);
