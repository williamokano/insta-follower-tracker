-- Records an explicit decision to accept an export that looks like it covers
-- only part of the follower list. Off by default: a partial export diffed
-- against a complete one invents unfollows for everybody it omits, so it is
-- refused unless the person uploading says otherwise.
ALTER TABLE uploads ADD COLUMN allow_partial INTEGER NOT NULL DEFAULT 0;
