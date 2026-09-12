package store

import (
	"context"
	"database/sql"
	"fmt"
)

// DepartureReason says what the export can work out about why somebody left the
// follower list.
//
// The export records membership, not causation. It lists who was present on the
// day it was generated and says nothing about why anyone is missing from the
// next one. What it does carry is several lists generated at the same moment,
// and reading a departure against the others narrows the possibilities.
//
// One answer is missing on purpose and cannot be added: Instagram never reports
// who blocked you. The blocked list in an export is the accounts you blocked.
type DepartureReason string

const (
	// DepartureYouBlocked: they are in your blocked list. Your own action, and
	// it explains the departure completely.
	DepartureYouBlocked DepartureReason = "you_blocked"

	// DepartureRenamed: the account is still there under a different name. See
	// classifyRenames for how a rename is recognised without a stable user id.
	DepartureRenamed DepartureReason = "renamed"

	// DepartureUnfollowed: they left your followers but you still follow them,
	// so the account still exists and is still visible to you. They simply
	// stopped following.
	DepartureUnfollowed DepartureReason = "unfollowed"

	// DepartureVanished: they left your followers and your following list in
	// the same execution. A plain unfollow never does that, so the account went
	// away as far as you are concerned: they blocked you, or deactivated,
	// deleted or were banned. The export cannot tell these apart, and saying
	// which would be a guess.
	DepartureVanished DepartureReason = "vanished"

	// DepartureUnknown: you do not follow them, so there is no second list to
	// read the departure against and nothing to say.
	DepartureUnknown DepartureReason = "unknown"
)

// Label renders a reason for a person to read.
func (d DepartureReason) Label() string {
	switch d {
	case DepartureYouBlocked:
		return "you blocked them"
	case DepartureRenamed:
		return "renamed"
	case DepartureUnfollowed:
		return "unfollowed you"
	case DepartureVanished:
		return "gone from Instagram"
	default:
		return "unknown"
	}
}

// classifyDepartures fills in departure_reason for everyone who left the
// follower list between two executions.
//
// Every branch reads only lists that both executions actually carried. An
// export that omitted a list says nothing about it, and treating that silence
// as an empty list would invent conclusions in the same way it would invent
// changes.
func classifyDepartures(ctx context.Context, tx *sql.Tx, uploadID, prevUploadID int64) error {
	if err := classifyRenames(ctx, tx, uploadID, prevUploadID); err != nil {
		return err
	}

	// Your own block explains the departure whatever the other lists say, so it
	// is settled first and the rest only fill in what is still unset. Only the
	// current execution needs the list: being in it now is the whole finding.
	blocked, err := recorded(ctx, tx, uploadID, ListKindBlocked)
	if err != nil {
		return err
	}
	if blocked {
		if _, err := tx.ExecContext(ctx, `
			UPDATE changes SET departure_reason = ?
			WHERE upload_id = ? AND list_kind = 'followers' AND change_type = 'unfollowed'
			  AND departure_reason IS NULL
			  AND user_id IN (
			      SELECT user_id FROM snapshot_members
			      WHERE upload_id = ? AND list_kind = 'blocked'
			  )`,
			string(DepartureYouBlocked), uploadID, uploadID); err != nil {
			return fmt.Errorf("classify blocked departures: %w", err)
		}
	}

	// The remaining two read the following list on both sides, so both
	// executions have to have carried it.
	following, err := bothRecorded(ctx, tx, uploadID, prevUploadID, ListKindFollowing)
	if err != nil {
		return err
	}
	if following {
		// Still in your following list, so the account is still there.
		if _, err := tx.ExecContext(ctx, `
			UPDATE changes SET departure_reason = ?
			WHERE upload_id = ? AND list_kind = 'followers' AND change_type = 'unfollowed'
			  AND departure_reason IS NULL
			  AND user_id IN (
			      SELECT user_id FROM snapshot_members
			      WHERE upload_id = ? AND list_kind = 'following'
			  )`,
			string(DepartureUnfollowed), uploadID, uploadID); err != nil {
			return fmt.Errorf("classify surviving departures: %w", err)
		}

		// Left your followers and your following in the same execution, which
		// an unfollow cannot do. Spelled out in full rather than leaning on the
		// statement above having claimed the survivors already.
		if _, err := tx.ExecContext(ctx, `
			UPDATE changes SET departure_reason = ?
			WHERE upload_id = ? AND list_kind = 'followers' AND change_type = 'unfollowed'
			  AND departure_reason IS NULL
			  AND user_id IN (
			      SELECT user_id FROM snapshot_members
			      WHERE upload_id = ? AND list_kind = 'following'
			  )
			  AND user_id NOT IN (
			      SELECT user_id FROM snapshot_members
			      WHERE upload_id = ? AND list_kind = 'following'
			  )`,
			string(DepartureVanished), uploadID, prevUploadID, uploadID); err != nil {
			return fmt.Errorf("classify vanished departures: %w", err)
		}
	}

	// Anything left has no evidence either way.
	if _, err := tx.ExecContext(ctx, `
		UPDATE changes SET departure_reason = ?
		WHERE upload_id = ? AND list_kind = 'followers' AND change_type = 'unfollowed'
		  AND departure_reason IS NULL`,
		string(DepartureUnknown), uploadID); err != nil {
		return fmt.Errorf("classify remaining departures: %w", err)
	}

	return nil
}

// recorded reports whether an execution carried a list at all. An export that
// omitted one says nothing about it, which is not the same as it being empty.
func recorded(ctx context.Context, tx *sql.Tx, uploadID int64, kind string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM upload_lists WHERE upload_id = ? AND list_kind = ?`,
		uploadID, kind).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check %s was recorded: %w", kind, err)
	}
	return n == 1, nil
}

// bothRecorded reports whether both executions carried a list.
func bothRecorded(ctx context.Context, tx *sql.Tx, uploadID, prevUploadID int64, kind string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM upload_lists WHERE list_kind = ? AND upload_id IN (?, ?)`,
		kind, uploadID, prevUploadID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check %s was recorded: %w", kind, err)
	}
	return n == 2, nil
}

// classifyRenames pairs a departure with an arrival that is the same person
// under a new name.
//
// The export has no stable user id: an entry is a username and a profile link
// built from that username, so a rename is indistinguishable from one account
// leaving and another arriving. What does survive a rename is the moment they
// followed you, which Instagram records per entry and which this database keeps
// as snapshot_members.followed_at.
//
// So a departure and an arrival sharing an exact follow timestamp is a rename.
// Two requirements keep that from being a coincidence dressed up as a fact:
// the timestamp must be present (HTML exports carry none, and a NULL matches
// nothing), and it must identify exactly one departure and one arrival. Where
// several share a timestamp the pairing is ambiguous, and an ambiguous rename
// is left unclassified rather than guessed.
func classifyRenames(ctx context.Context, tx *sql.Tx, uploadID, prevUploadID int64) error {
	rows, err := tx.QueryContext(ctx, `
		WITH gone AS (
		    SELECT c.user_id, prev.followed_at
		    FROM changes c
		    JOIN snapshot_members prev
		      ON prev.upload_id = ? AND prev.list_kind = 'followers' AND prev.user_id = c.user_id
		    WHERE c.upload_id = ? AND c.list_kind = 'followers' AND c.change_type = 'unfollowed'
		      AND prev.followed_at IS NOT NULL
		),
		arrived AS (
		    SELECT c.user_id, cur.followed_at
		    FROM changes c
		    JOIN snapshot_members cur
		      ON cur.upload_id = ? AND cur.list_kind = 'followers' AND cur.user_id = c.user_id
		    WHERE c.upload_id = ? AND c.list_kind = 'followers' AND c.change_type = 'followed'
		      AND cur.followed_at IS NOT NULL
		)
		SELECT gone.user_id, arrived.user_id
		FROM gone
		JOIN arrived ON arrived.followed_at = gone.followed_at
		WHERE (SELECT COUNT(*) FROM gone g WHERE g.followed_at = gone.followed_at) = 1
		  AND (SELECT COUNT(*) FROM arrived a WHERE a.followed_at = gone.followed_at) = 1`,
		prevUploadID, uploadID, uploadID, uploadID)
	if err != nil {
		return fmt.Errorf("pair renames: %w", err)
	}
	defer rows.Close()

	type rename struct{ from, to int64 }
	var pairs []rename
	for rows.Next() {
		var p rename
		if err := rows.Scan(&p.from, &p.to); err != nil {
			return fmt.Errorf("scan rename pair: %w", err)
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read rename pairs: %w", err)
	}

	for _, p := range pairs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE changes SET departure_reason = ?, renamed_to_user_id = ?
			WHERE upload_id = ? AND list_kind = 'followers'
			  AND change_type = 'unfollowed' AND user_id = ?`,
			string(DepartureRenamed), p.to, uploadID, p.from); err != nil {
			return fmt.Errorf("mark rename departure: %w", err)
		}

		// The arrival is the same person, so it is not a new follower either.
		if _, err := tx.ExecContext(ctx, `
			UPDATE changes SET renamed_from_user_id = ?
			WHERE upload_id = ? AND list_kind = 'followers'
			  AND change_type = 'followed' AND user_id = ?`,
			p.from, uploadID, p.to); err != nil {
			return fmt.Errorf("mark rename arrival: %w", err)
		}
	}

	return nil
}
