-- Accounts are identified by the Instagram handle supplied at upload time;
-- the export itself does not reliably carry the owner's username.
CREATE TABLE accounts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    handle     TEXT    NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
);

-- One row per distinct follower ever seen for an account. Usernames are
-- stored lowercased because Instagram handles are case insensitive.
CREATE TABLE users (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    username   TEXT    NOT NULL,
    href       TEXT    NOT NULL DEFAULT '',
    UNIQUE (account_id, username)
);

-- An upload is one execution: a single exported follower list. sequence_no is
-- assigned when processing succeeds, so it always reflects processing order.
CREATE TABLE uploads (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id        INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    sequence_no       INTEGER,
    original_filename TEXT    NOT NULL,
    stored_path       TEXT    NOT NULL,
    sha256            TEXT    NOT NULL DEFAULT '',
    size_bytes        INTEGER NOT NULL DEFAULT 0,
    status            TEXT    NOT NULL CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    error_message     TEXT    NOT NULL DEFAULT '',
    uploaded_at       INTEGER NOT NULL,
    started_at        INTEGER,
    processed_at      INTEGER,
    follower_count    INTEGER NOT NULL DEFAULT 0,
    added_count       INTEGER NOT NULL DEFAULT 0,
    removed_count     INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX idx_uploads_account_sequence
    ON uploads (account_id, sequence_no) WHERE sequence_no IS NOT NULL;
CREATE INDEX idx_uploads_account_uploaded ON uploads (account_id, uploaded_at DESC);
CREATE INDEX idx_uploads_status ON uploads (status, uploaded_at);

-- The full membership of every snapshot. Keeping complete sets (rather than
-- only deltas) is what makes an exact diff between any two executions
-- possible, including the case where a follower leaves and later returns.
CREATE TABLE snapshot_members (
    upload_id   INTEGER NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    followed_at INTEGER,
    PRIMARY KEY (upload_id, user_id)
) WITHOUT ROWID;

CREATE INDEX idx_snapshot_members_user ON snapshot_members (user_id);

-- Materialised deltas between consecutive executions.
CREATE TABLE changes (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id     INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    upload_id      INTEGER NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
    prev_upload_id INTEGER          REFERENCES uploads(id) ON DELETE SET NULL,
    user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    change_type    TEXT    NOT NULL CHECK (change_type IN ('followed', 'unfollowed')),
    created_at     INTEGER NOT NULL
);

CREATE INDEX idx_changes_upload_type ON changes (upload_id, change_type);
CREATE INDEX idx_changes_account_type ON changes (account_id, change_type);
CREATE INDEX idx_changes_user_type ON changes (user_id, change_type);
