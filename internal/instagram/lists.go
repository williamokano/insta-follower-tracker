package instagram

import "regexp"

// ListKind names one of the relationship lists an export contains.
//
// The values are stable identifiers used in storage and in URLs, deliberately
// not the file names: those drifted between export versions, with
// blocked_accounts becoming blocked_profiles and recently_unfollowed_accounts
// becoming recently_unfollowed_profiles.
type ListKind string

// The lists this service reads.
const (
	ListFollowers          ListKind = "followers"
	ListFollowing          ListKind = "following"
	ListCloseFriends       ListKind = "close_friends"
	ListPendingRequests    ListKind = "pending_follow_requests"
	ListRecentRequests     ListKind = "recent_follow_requests"
	ListRecentlyUnfollowed ListKind = "recently_unfollowed"
	ListBlocked            ListKind = "blocked"
	ListRestricted         ListKind = "restricted"
	ListRemovedSuggestions ListKind = "removed_suggestions"
	ListFavourited         ListKind = "favourited"
)

// listDefinition ties a kind to the file names that carry it and to how it
// should be described.
type listDefinition struct {
	Kind ListKind
	// Pattern matches the entry within an export archive, at any depth.
	Pattern *regexp.Regexp
	// Label is how the list is named in the interface.
	Label string
	// Description explains what the list is, since several are easy to
	// confuse with one another.
	Description string
	// Tracked lists are diffed over time. Untracked ones are parsed but not
	// followed, because change in them is not meaningful.
	Tracked bool
}

// listDefinitions is the full set, in the order they are presented.
//
// following_hashtags is deliberately absent: its entries are topics rather than
// accounts, so it does not belong in a model built around usernames.
var listDefinitions = []listDefinition{
	{
		Kind:        ListFollowers,
		Pattern:     regexp.MustCompile(`(?i)(^|/)followers(_\d+)?\.(json|html?)$`),
		Label:       "Followers",
		Description: "Accounts that follow you.",
		Tracked:     true,
	},
	{
		Kind:        ListFollowing,
		Pattern:     regexp.MustCompile(`(?i)(^|/)following(_\d+)?\.(json|html?)$`),
		Label:       "Following",
		Description: "Accounts you follow.",
		Tracked:     true,
	},
	{
		Kind:        ListCloseFriends,
		Pattern:     regexp.MustCompile(`(?i)(^|/)close_friends(_\d+)?\.(json|html?)$`),
		Label:       "Close friends",
		Description: "Your close friends list.",
		Tracked:     true,
	},
	{
		Kind:        ListPendingRequests,
		Pattern:     regexp.MustCompile(`(?i)(^|/)pending_follow_requests(_\d+)?\.(json|html?)$`),
		Label:       "Pending requests",
		Description: "Follow requests you have sent that are still unanswered.",
		Tracked:     true,
	},
	{
		Kind:        ListRecentRequests,
		Pattern:     regexp.MustCompile(`(?i)(^|/)recent_follow_requests(_\d+)?\.(json|html?)$`),
		Label:       "Recent requests",
		Description: "Follow requests you sent recently.",
		Tracked:     false,
	},
	{
		Kind: ListRecentlyUnfollowed,
		// Instagram renamed this between export versions.
		Pattern:     regexp.MustCompile(`(?i)(^|/)recently_unfollowed_(profiles|accounts)(_\d+)?\.(json|html?)$`),
		Label:       "Recently unfollowed",
		Description: "Accounts you stopped following recently, as Instagram reports them.",
		Tracked:     false,
	},
	{
		Kind:        ListBlocked,
		Pattern:     regexp.MustCompile(`(?i)(^|/)blocked_(profiles|accounts)(_\d+)?\.(json|html?)$`),
		Label:       "Blocked",
		Description: "Accounts you have blocked.",
		Tracked:     true,
	},
	{
		Kind:        ListRestricted,
		Pattern:     regexp.MustCompile(`(?i)(^|/)restricted_(profiles|accounts)(_\d+)?\.(json|html?)$`),
		Label:       "Restricted",
		Description: "Accounts you have restricted.",
		Tracked:     true,
	},
	{
		Kind:        ListRemovedSuggestions,
		Pattern:     regexp.MustCompile(`(?i)(^|/)removed_suggestions(_\d+)?\.(json|html?)$`),
		Label:       "Removed suggestions",
		Description: "Suggested accounts you dismissed.",
		Tracked:     false,
	},
	{
		Kind: ListFavourited,
		// The real file name contains an apostrophe.
		Pattern:     regexp.MustCompile(`(?i)(^|/)profiles_you've_favou?rited(_\d+)?\.(json|html?)$`),
		Label:       "Favourited",
		Description: "Accounts you have favourited.",
		Tracked:     true,
	},
}

// ListInfo describes a kind for presentation.
type ListInfo struct {
	Kind        ListKind `json:"kind"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Tracked     bool     `json:"tracked"`
}

// Lists returns every kind this service understands, in presentation order.
func Lists() []ListInfo {
	out := make([]ListInfo, 0, len(listDefinitions))
	for _, d := range listDefinitions {
		out = append(out, ListInfo{Kind: d.Kind, Label: d.Label, Description: d.Description, Tracked: d.Tracked})
	}
	return out
}

// LookupList finds a kind by its identifier.
func LookupList(kind string) (ListInfo, bool) {
	for _, d := range listDefinitions {
		if string(d.Kind) == kind {
			return ListInfo{Kind: d.Kind, Label: d.Label, Description: d.Description, Tracked: d.Tracked}, true
		}
	}
	return ListInfo{}, false
}

// kindForEntry reports which list an archive entry carries, if any.
//
// Order matters: "following" must be tested after "followers" would have
// matched, which the distinct patterns already ensure, but the loop keeps the
// precedence explicit and stable.
func kindForEntry(name string) (ListKind, bool) {
	for _, d := range listDefinitions {
		if d.Pattern.MatchString(name) {
			return d.Kind, true
		}
	}
	return "", false
}
