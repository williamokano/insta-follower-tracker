-- An export carries several relationship lists, not only followers: who you
-- follow, your close friends, pending requests, blocked and restricted
-- accounts. Each is a set of accounts that changes over time in exactly the way
-- followers do, so each is recorded and diffed the same way.

-- snapshot_members needs the list in its primary key, which SQLite cannot add
-- in place, so the table is rebuilt. Everything recorded so far is the follower
-- list.
CREATE TABLE snapshot_members_new (
    upload_id   INTEGER NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
    list_kind   TEXT    NOT NULL,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    followed_at INTEGER,
    PRIMARY KEY (upload_id, list_kind, user_id)
) WITHOUT ROWID;

INSERT INTO snapshot_members_new (upload_id, list_kind, user_id, followed_at)
SELECT upload_id, 'followers', user_id, followed_at FROM snapshot_members;

DROP TABLE snapshot_members;
ALTER TABLE snapshot_members_new RENAME TO snapshot_members;

CREATE INDEX idx_snapshot_members_user ON snapshot_members (user_id);
CREATE INDEX idx_snapshot_members_kind ON snapshot_members (upload_id, list_kind);

-- changes only needs a new column; existing rows are all follower changes.
ALTER TABLE changes ADD COLUMN list_kind TEXT NOT NULL DEFAULT 'followers';
CREATE INDEX idx_changes_upload_kind ON changes (upload_id, list_kind, change_type);

-- Per-list totals for an execution. uploads.follower_count and its siblings
-- stay as they are and continue to describe the follower list specifically,
-- which is what every existing reader of them means.
CREATE TABLE upload_lists (
    upload_id     INTEGER NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
    list_kind     TEXT    NOT NULL,
    member_count  INTEGER NOT NULL DEFAULT 0,
    added_count   INTEGER NOT NULL DEFAULT 0,
    removed_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (upload_id, list_kind)
) WITHOUT ROWID;

INSERT INTO upload_lists (upload_id, list_kind, member_count, added_count, removed_count)
SELECT id, 'followers', follower_count, added_count, removed_count
FROM uploads WHERE status = 'completed';
