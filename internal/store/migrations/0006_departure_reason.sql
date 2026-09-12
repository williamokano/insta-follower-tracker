-- Why somebody left the follower list, as far as the export can tell.
--
-- The export records membership, never causation: it says who was in a list on
-- the day it was generated and nothing about why anyone is missing from the
-- next one. Some of the answer is nevertheless recoverable by reading the other
-- lists in the same execution, which is what this column holds.
--
-- Instagram never reports who blocked you, in the export or anywhere else, so
-- "they blocked me" is not one of the values here and cannot be.
ALTER TABLE changes ADD COLUMN departure_reason TEXT;

-- The account a departure turned out to be, when a rename is recognised. The
-- export has no stable user id -- entries are keyed by username -- so a rename
-- looks exactly like one person leaving and another arriving. What survives a
-- rename is the follow timestamp, and that is what pairs the two.
ALTER TABLE changes ADD COLUMN renamed_to_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE changes ADD COLUMN renamed_from_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX idx_changes_departure ON changes (upload_id, departure_reason);
