// Package instagram parses follower lists out of Instagram's official
// "Download your information" export.
//
// Two input shapes are supported: the export archive as downloaded, and a bare
// followers_N.json lifted out of it.
package instagram

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// ErrNoFollowers reports that the input parsed successfully but contained no
// follower list, which almost always means the wrong file was uploaded.
var ErrNoFollowers = errors.New("no follower list found in this file")

// ArchiveContentsError explains why an archive yielded no follower list, by
// naming what it actually held.
//
// The bare sentinel is useless to somebody holding a real export: it says the
// file is wrong without saying what would be right. Listing the entries that
// were nearly matches turns a support question into a self-diagnosis.
type ArchiveContentsError struct {
	// Entries is how many members the archive had.
	Entries int
	// NearMisses are entry names that look related but are not the follower
	// list, such as following or close_friends.
	NearMisses []string
}

func (e *ArchiveContentsError) Error() string {
	msg := fmt.Sprintf(
		"no follower list found: searched %d archive entries for followers_*.json or followers_*.html and found none",
		e.Entries)
	if len(e.NearMisses) > 0 {
		msg += fmt.Sprintf(" (the archive does contain %s, which are different lists)",
			strings.Join(e.NearMisses, ", "))
	}
	return msg
}

// Unwrap keeps errors.Is(err, ErrNoFollowers) working for callers that only
// care that the upload was not a follower list.
func (e *ArchiveContentsError) Unwrap() error { return ErrNoFollowers }

// Limits applied while reading an upload. They bound the work a hostile or
// simply malformed archive can cause.
const (
	// MaxArchiveEntries caps how many archive members are inspected.
	MaxArchiveEntries = 5000
	// MaxDecompressedBytes caps the total decompressed bytes read from an archive.
	MaxDecompressedBytes = 200 << 20 // 200 MiB
	// MaxJSONBytes caps a single JSON document.
	MaxJSONBytes = 200 << 20
)

// Follower is one account from a follower list.
type Follower struct {
	Username string
	Href     string
	// FollowedAt is the timestamp Instagram recorded for the follow, in Unix
	// seconds. It is nil when the export omits it.
	FollowedAt *int64
}

// entry mirrors the repeated element Instagram uses throughout the export.
type entry struct {
	StringListData []struct {
		Href      string `json:"href"`
		Value     string `json:"value"`
		Timestamp int64  `json:"timestamp"`
	} `json:"string_list_data"`
}

// Export is one parsed download: every relationship list it carried, together
// with what could be determined about how much of them it represents.
type Export struct {
	// Lists holds the members of each list the export contained. A kind that
	// the export did not carry is absent rather than empty.
	Lists map[ListKind][]Follower
	// Coverage reports whether the export looks like the complete follower
	// list or only a date-limited slice of it.
	Coverage Coverage
	// TakenAt is when the export was generated, as far as the file itself
	// says. Zero when nothing in it reveals that.
	TakenAt time.Time
	// TakenAtSource names where TakenAt came from.
	TakenAtSource string
	// Owner is the account the export was generated for, when it says so.
	// Empty otherwise: the follower list itself never identifies its owner.
	Owner string
}

// Followers is the list this service is built around, and the only one an
// export must contain to be usable.
func (e *Export) Followers() []Follower { return e.Lists[ListFollowers] }

// Kinds returns the lists this export carried, in presentation order.
func (e *Export) Kinds() []ListKind {
	out := make([]ListKind, 0, len(e.Lists))
	for _, d := range listDefinitions {
		if _, ok := e.Lists[d.Kind]; ok {
			out = append(out, d.Kind)
		}
	}
	return out
}

// Parse reads a follower list from an uploaded file. The archive form is
// detected from the content itself rather than the file name, so a renamed
// export still works.
func Parse(r io.ReaderAt, size int64) (*Export, error) {
	if size <= 0 {
		return nil, ErrNoFollowers
	}

	if isZip(r) {
		return parseArchive(r, size)
	}

	body, err := readAllLimited(io.NewSectionReader(r, 0, size), MaxJSONBytes)
	if err != nil {
		return nil, err
	}
	followers, recognised, err := parseDocument(body)
	if err != nil {
		return nil, err
	}
	// A bare file names no list, so it is taken to be the follower list: that
	// is what somebody lifting a single file out of an archive is almost
	// always uploading. An empty one is treated as a mistake rather than as a
	// genuine claim of having no followers, since there is nothing else in the
	// upload to corroborate it.
	if !recognised || len(followers) == 0 {
		return nil, ErrNoFollowers
	}
	// It also carries no start_here page, so nothing in it describes how much
	// of the list it holds.
	return &Export{Lists: map[ListKind][]Follower{ListFollowers: dedupe(followers)}}, nil
}

// parseDocument reads one relationship list, in whichever of the two download
// formats it happens to be. The format is detected from the content, so a file
// renamed on the way out of the archive still parses.
//
// It reports whether the document was recognised as a list at all, separately
// from how many entries that list held. The distinction matters: an empty
// pending-requests file means you have no pending requests, which is a fact
// worth recording, while an unrecognised file means something else entirely.
func parseDocument(body []byte) (followers []Follower, recognised bool, err error) {
	if looksLikeHTML(body) {
		return parseHTMLExport(body)
	}
	return parseJSON(body)
}

// isZip checks for the local file header magic that starts every zip archive.
func isZip(r io.ReaderAt) bool {
	var magic [4]byte
	if n, err := r.ReadAt(magic[:], 0); n < 4 || (err != nil && !errors.Is(err, io.EOF)) {
		return false
	}
	return bytes.Equal(magic[:], []byte{'P', 'K', 0x03, 0x04})
}

// parseJSON handles the two document shapes Instagram emits for relationship
// lists: a bare array, and an object wrapping that array under a
// relationships_* key.
func parseJSON(body []byte) ([]Follower, bool, error) {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 {
		return nil, false, nil
	}

	switch trimmed[0] {
	case '[':
		var entries []entry
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			return nil, false, fmt.Errorf("parse follower array: %w", err)
		}
		return flatten(entries), true, nil

	case '{':
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &wrapper); err != nil {
			return nil, false, fmt.Errorf("parse follower object: %w", err)
		}

		// Prefer an explicit relationships_* key, then fall back to any key
		// holding an array, so a renamed wrapper still parses.
		keys := make([]string, 0, len(wrapper))
		for k := range wrapper {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			ri := strings.HasPrefix(keys[i], "relationships_")
			rj := strings.HasPrefix(keys[j], "relationships_")
			if ri != rj {
				return ri
			}
			return keys[i] < keys[j]
		})

		// A relationships_* key is the list even when it is empty; any other
		// key only counts if it actually holds entries.
		for _, k := range keys {
			var entries []entry
			if err := json.Unmarshal(wrapper[k], &entries); err != nil {
				continue
			}
			followers := flatten(entries)
			if len(followers) > 0 || strings.HasPrefix(k, "relationships_") {
				return followers, true, nil
			}
		}
		return nil, false, nil

	default:
		return nil, false, nil
	}
}

func flatten(entries []entry) []Follower {
	out := make([]Follower, 0, len(entries))
	for _, e := range entries {
		for _, item := range e.StringListData {
			username := normalizeUsername(item.Value)
			if username == "" {
				username = usernameFromHref(item.Href)
			}
			if username == "" {
				continue
			}

			f := Follower{Username: username, Href: item.Href}
			if item.Timestamp > 0 {
				ts := item.Timestamp
				f.FollowedAt = &ts
			}
			out = append(out, f)
		}
	}
	return out
}

func normalizeUsername(v string) string {
	u := strings.ToLower(strings.TrimSpace(v))
	u = strings.TrimPrefix(u, "@")
	return strings.TrimSuffix(u, "/")
}

// usernameFromHref recovers the handle from a profile URL, for the occasional
// entry that carries a link but a blank value.
func usernameFromHref(href string) string {
	h := strings.TrimSpace(href)
	if h == "" {
		return ""
	}
	if idx := strings.IndexAny(h, "?#"); idx >= 0 {
		h = h[:idx]
	}
	h = strings.TrimSuffix(h, "/")
	if idx := strings.LastIndex(h, "/"); idx >= 0 {
		h = h[idx+1:]
	}
	return normalizeUsername(h)
}

// dedupe collapses repeats, which occur naturally when a multi-part export is
// recombined, and sorts for stable downstream comparisons.
func dedupe(followers []Follower) []Follower {
	seen := make(map[string]int, len(followers))
	out := make([]Follower, 0, len(followers))

	for _, f := range followers {
		if idx, ok := seen[f.Username]; ok {
			// Keep whichever copy carries the richer metadata.
			if out[idx].Href == "" && f.Href != "" {
				out[idx].Href = f.Href
			}
			if out[idx].FollowedAt == nil && f.FollowedAt != nil {
				out[idx].FollowedAt = f.FollowedAt
			}
			continue
		}
		seen[f.Username] = len(out)
		out = append(out, f)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read upload: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("file is larger than the %d byte parsing limit", limit)
	}
	return body, nil
}
