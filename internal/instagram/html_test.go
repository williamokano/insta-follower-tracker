package instagram_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/williamokano/insta-follower-tracker/internal/instagram"
)

// TestParseHTMLExport covers the download format that is offered alongside
// JSON, and that a real export turned out to use.
func TestParseHTMLExport(t *testing.T) {
	got, err := parse(t, fixture(t, "followers_1.html"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	assertNames(t, got, "alice", "bob", "carol_x.99")
}

// TestParseHTMLIgnoresChromeLinks pins the filtering that keeps the page's own
// navigation out of the follower list.
func TestParseHTMLIgnoresChromeLinks(t *testing.T) {
	got, err := parse(t, fixture(t, "followers_1.html"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, f := range got {
		switch f.Username {
		case "explore", "p", "help", "":
			t.Fatalf("%q is page furniture, not a follower", f.Username)
		}
	}
}

func TestParseHTMLRejectsNonProfileLinks(t *testing.T) {
	page := []byte(`<html><body>
		<a href="https://www.instagram.com/">logo</a>
		<a href="https://www.instagram.com/p/Cx1y2z3AbCd/">post</a>
		<a href="https://www.instagram.com/reel/Abc123/">reel</a>
		<a href="https://www.instagram.com/stories/someone/123/">story</a>
		<a href="https://example.com/alice">elsewhere</a>
		<a href="https://help.instagram.com/legal">help</a>
		<a href="mailto:someone@example.com">mail</a>
	</body></html>`)

	if _, err := parse(t, page); !errors.Is(err, instagram.ErrNoFollowers) {
		t.Fatalf("got %v, want ErrNoFollowers: none of these links are followers", err)
	}
}

// TestParseHTMLIsDetectedByContent matters because the archive entry may be
// renamed, and because a bare upload carries no format hint at all.
func TestParseHTMLIsDetectedByContent(t *testing.T) {
	page := []byte("\n\n  <!DOCTYPE html><html><body>" +
		`<a href="https://www.instagram.com/dave">dave</a>` +
		"</body></html>")

	got, err := parse(t, page)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	assertNames(t, got, "dave")
}

// TestParseHTMLOmitsFollowDates documents a deliberate limitation: the HTML
// export renders dates in the account's own language, so they are not guessed
// at. They are display metadata only and never affect a diff.
func TestParseHTMLOmitsFollowDates(t *testing.T) {
	got, err := parse(t, fixture(t, "followers_1.html"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, f := range got {
		if f.FollowedAt != nil {
			t.Fatalf("%s carries a follow date; the html export's dates are localised prose", f.Username)
		}
	}
}

// TestArchiveErrorNamesWhatItFound is the diagnostic that was missing: an
// archive holding the neighbouring relationship lists but no follower list
// should say so, rather than only that something was wrong.
func TestArchiveErrorNamesWhatItFound(t *testing.T) {
	archive := buildZip(t, map[string][]byte{
		"followers_and_following/following.html":                    []byte("<html></html>"),
		"followers_and_following/close_friends.html":                []byte("<html></html>"),
		"followers_and_following/recently_unfollowed_profiles.html": []byte("<html></html>"),
		"files/Instagram-Logo.png":                                  {0x89, 'P', 'N', 'G'},
	})

	_, err := parse(t, archive)
	if !errors.Is(err, instagram.ErrNoFollowers) {
		t.Fatalf("got %v, want it to satisfy errors.Is(ErrNoFollowers)", err)
	}

	msg := err.Error()
	if !strings.Contains(msg, "followers_*.html") {
		t.Fatalf("error should name what it searched for, got: %s", msg)
	}
	if !strings.Contains(msg, "following.html") {
		t.Fatalf("error should name the near misses it did find, got: %s", msg)
	}

	var contents *instagram.ArchiveContentsError
	if !errors.As(err, &contents) {
		t.Fatal("error should be inspectable as *ArchiveContentsError")
	}
	if contents.Entries != 4 {
		t.Fatalf("Entries = %d, want 4", contents.Entries)
	}
}

// TestParseRealWorldHTMLExportLayout mirrors the archive a real "followers and
// following" HTML download produces: the follower list sits among eight other
// relationship lists with similar names, plus a bundled logo asset.
func TestParseRealWorldHTMLExportLayout(t *testing.T) {
	other := []byte(`<html><body><a href="https://www.instagram.com/someoneelse">someoneelse</a></body></html>`)

	archive := buildZip(t, map[string][]byte{
		"followers_and_following/followers_1.html":                  fixture(t, "followers_1.html"),
		"followers_and_following/following.html":                    other,
		"followers_and_following/close_friends.html":                other,
		"followers_and_following/pending_follow_requests.html":      other,
		"followers_and_following/recent_follow_requests.html":       other,
		"followers_and_following/recently_unfollowed_profiles.html": other,
		"followers_and_following/removed_suggestions.html":          other,
		"followers_and_following/restricted_profiles.html":          other,
		"followers_and_following/profiles_you've_favorited.html":    other,
		"files/Instagram-Logo.png":                                  {0x89, 'P', 'N', 'G'},
	})

	got, err := parse(t, archive)
	if err != nil {
		t.Fatalf("parse archive: %v", err)
	}

	// Only followers_1.html contributes. "someoneelse" appears in every other
	// list and must not leak in from any of them.
	assertNames(t, got, "alice", "bob", "carol_x.99")
}
