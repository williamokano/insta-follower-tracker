package store

import "time"

// UploadStatus tracks an upload through the asynchronous processing pipeline.
type UploadStatus string

// Upload lifecycle states.
const (
	StatusPending    UploadStatus = "pending"
	StatusProcessing UploadStatus = "processing"
	StatusCompleted  UploadStatus = "completed"
	StatusFailed     UploadStatus = "failed"
)

// ChangeType distinguishes the two kinds of delta recorded between executions.
type ChangeType string

// Change kinds.
const (
	ChangeFollowed   ChangeType = "followed"
	ChangeUnfollowed ChangeType = "unfollowed"
)

// DefaultListKind is the list every query falls back to, and the only one the
// service tracked before other lists were read.
const DefaultListKind = "followers"

// The list kinds this package reasons about by name. The full set and its
// presentation live in the instagram package; these are the few that queries
// here cross-reference, repeated rather than imported to keep storage from
// depending on parsing.
const (
	ListKindFollowers = "followers"
	ListKindFollowing = "following"
	ListKindBlocked   = "blocked"
)

// ListTotals are one relationship list's numbers for one execution.
type ListTotals struct {
	Kind         string `json:"kind"`
	MemberCount  int    `json:"member_count"`
	AddedCount   int    `json:"added_count"`
	RemovedCount int    `json:"removed_count"`
}

// SourceProcessingOrder marks an execution whose date is not known from the
// export, only from the order it happened to be processed in. Executions
// recorded before export dates existed carry this.
const SourceProcessingOrder = "processing order"

// Account is one tracked Instagram handle.
type Account struct {
	ID        int64     `json:"id"`
	Handle    string    `json:"handle"`
	CreatedAt time.Time `json:"created_at"`
}

// AccountSummary decorates an account with headline counts for the UI.
type AccountSummary struct {
	Account
	UploadCount    int        `json:"upload_count"`
	CompletedCount int        `json:"completed_count"`
	FollowerCount  int        `json:"follower_count"`
	LastUploadAt   *time.Time `json:"last_upload_at"`
}

// Upload is a single execution: one uploaded follower export.
type Upload struct {
	ID               int64        `json:"id"`
	AccountID        int64        `json:"account_id"`
	AccountHandle    string       `json:"account_handle"`
	SequenceNo       *int64       `json:"sequence_no"`
	OriginalFilename string       `json:"original_filename"`
	StoredPath       string       `json:"-"`
	SHA256           string       `json:"sha256"`
	SizeBytes        int64        `json:"size_bytes"`
	Status           UploadStatus `json:"status"`
	ErrorMessage     string       `json:"error_message,omitempty"`
	UploadedAt       time.Time    `json:"uploaded_at"`
	StartedAt        *time.Time   `json:"started_at"`
	ProcessedAt      *time.Time   `json:"processed_at"`
	FollowerCount    int          `json:"follower_count"`
	AddedCount       int          `json:"added_count"`
	RemovedCount     int          `json:"removed_count"`
	// IsBaseline is true for the first execution of an account, which has no
	// predecessor to diff against.
	IsBaseline bool `json:"is_baseline"`
	// AllowPartial records that this upload was accepted despite looking like
	// it covers only part of the follower list.
	AllowPartial bool `json:"allow_partial"`
	// SnapshotTakenAt is when the export was generated. Executions are ordered
	// by this rather than by upload time, so an older export uploaded later
	// still sorts into its rightful place.
	SnapshotTakenAt *time.Time `json:"snapshot_taken_at"`
	// SnapshotSource names where SnapshotTakenAt came from.
	SnapshotSource string `json:"snapshot_source"`
	// SnapshotDate is a date supplied at upload time, overriding whatever the
	// archive says. Zero when none was given.
	SnapshotDate time.Time `json:"-"`
}

// Member is one follower within a snapshot, as handed to ApplySnapshot.
type Member struct {
	Username   string
	Href       string
	FollowedAt *int64
}

// Follower is a follower returned from a query.
type Follower struct {
	Username   string     `json:"username"`
	Href       string     `json:"href,omitempty"`
	FollowedAt *time.Time `json:"followed_at,omitempty"`
}

// Change is one follow/unfollow event attributed to an execution.
type Change struct {
	Username     string     `json:"username"`
	Href         string     `json:"href,omitempty"`
	ChangeType   ChangeType `json:"change_type"`
	UploadID     int64      `json:"upload_id"`
	SequenceNo   *int64     `json:"sequence_no"`
	PrevUploadID *int64     `json:"prev_upload_id"`
	DetectedAt   time.Time  `json:"detected_at"`
	// Reason says what the export could work out about a departure. Empty on
	// arrivals, and on departures from lists other than followers.
	Reason DepartureReason `json:"departure_reason,omitempty"`
	// ReasonLabel is Reason in words, so a reader does not have to know the
	// identifiers.
	ReasonLabel string `json:"departure_label,omitempty"`
	// RenamedTo is the name this account now goes by, when Reason is
	// DepartureRenamed. RenamedFrom is the reverse, set on the arrival that
	// turned out to be the same person.
	RenamedTo   string `json:"renamed_to,omitempty"`
	RenamedFrom string `json:"renamed_from,omitempty"`
}

// SnapshotResult reports what ApplySnapshot recorded for an execution.
type SnapshotResult struct {
	UploadID      int64  `json:"upload_id"`
	SequenceNo    int64  `json:"sequence_no"`
	PrevUploadID  *int64 `json:"prev_upload_id"`
	FollowerCount int    `json:"follower_count"`
	AddedCount    int    `json:"added_count"`
	RemovedCount  int    `json:"removed_count"`
	IsBaseline    bool   `json:"is_baseline"`
}
