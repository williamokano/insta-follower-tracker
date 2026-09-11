package instagram_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/williamokano/insta-follower-tracker/internal/instagram"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

func parse(t *testing.T, body []byte) ([]instagram.Follower, error) {
	t.Helper()
	export, err := instagram.Parse(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, err
	}
	return export.Followers, nil
}

// parseExport keeps the full result for tests that care about coverage.
func parseExport(t *testing.T, body []byte) (*instagram.Export, error) {
	t.Helper()
	return instagram.Parse(bytes.NewReader(body), int64(len(body)))
}

func names(followers []instagram.Follower) []string {
	out := make([]string, 0, len(followers))
	for _, f := range followers {
		out = append(out, f.Username)
	}
	return out
}

func assertNames(t *testing.T, got []instagram.Follower, want ...string) {
	t.Helper()
	list := names(got)
	if len(list) != len(want) {
		t.Fatalf("got %v, want %v", list, want)
	}
	for i := range want {
		if list[i] != want[i] {
			t.Fatalf("got %v, want %v", list, want)
		}
	}
}

// buildZip assembles an archive in memory from a path -> contents map.
func buildZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestParseBareArrayExport(t *testing.T) {
	got, err := parse(t, fixture(t, "followers_array.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	assertNames(t, got, "alice", "bob", "carol")

	// Usernames are lowercased so the comparison is case insensitive, and the
	// handle is recovered from the profile link when the value is blank.
	for _, f := range got {
		if f.Username == "alice" {
			if f.FollowedAt == nil || *f.FollowedAt != 1700000000 {
				t.Fatalf("alice followed_at = %v, want 1700000000", f.FollowedAt)
			}
		}
		if f.Username == "carol" && f.FollowedAt != nil {
			t.Fatal("carol has no timestamp in the export and should keep none")
		}
	}
}

func TestParseWrappedObjectExport(t *testing.T) {
	got, err := parse(t, fixture(t, "followers_wrapped.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	assertNames(t, got, "dave", "erin")
}

func TestParseArchiveFindsNestedFollowerList(t *testing.T) {
	archive := buildZip(t, map[string][]byte{
		"connections/followers_and_following/followers_1.json": fixture(t, "followers_array.json"),
		"personal_information/personal_information.json":       []byte(`{"unrelated": true}`),
		"media/posts/photo.jpg":                                bytes.Repeat([]byte{0xff}, 64),
	})

	got, err := parse(t, archive)
	if err != nil {
		t.Fatalf("parse archive: %v", err)
	}
	assertNames(t, got, "alice", "bob", "carol")
}

func TestParseArchiveMergesMultiPartFollowerLists(t *testing.T) {
	archive := buildZip(t, map[string][]byte{
		"connections/followers_and_following/followers_1.json": fixture(t, "followers_array.json"),
		"connections/followers_and_following/followers_2.json": fixture(t, "followers_part2.json"),
	})

	got, err := parse(t, archive)
	if err != nil {
		t.Fatalf("parse archive: %v", err)
	}
	// alice appears in both parts and must be counted once.
	assertNames(t, got, "alice", "bob", "carol", "frank")
}

func TestParseArchiveIgnoresFollowingList(t *testing.T) {
	archive := buildZip(t, map[string][]byte{
		"connections/followers_and_following/followers_1.json": fixture(t, "followers_array.json"),
		"connections/followers_and_following/following.json":   fixture(t, "followers_wrapped.json"),
	})

	got, err := parse(t, archive)
	if err != nil {
		t.Fatalf("parse archive: %v", err)
	}
	// dave and erin come from following.json and must not leak in.
	assertNames(t, got, "alice", "bob", "carol")
}

func TestParseDetectsArchiveByContentNotExtension(t *testing.T) {
	archive := buildZip(t, map[string][]byte{
		"followers_1.json": fixture(t, "followers_wrapped.json"),
	})

	// Parse is never told the file name, so a renamed export still works.
	got, err := parse(t, archive)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	assertNames(t, got, "dave", "erin")
}

func TestParseRejectsNonExportInput(t *testing.T) {
	cases := map[string][]byte{
		"unrelated json object": fixture(t, "not_an_export.json"),
		"empty file":            {},
		"empty array":           []byte(`[]`),
		"plain text":            []byte("this is not json at all"),
		"archive without list": buildZip(t, map[string][]byte{
			"notes.txt": []byte("nothing to see"),
		}),
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parse(t, body)
			if !errors.Is(err, instagram.ErrNoFollowers) {
				t.Fatalf("got %v, want ErrNoFollowers", err)
			}
		})
	}
}

func TestParseReportsMalformedJSON(t *testing.T) {
	_, err := parse(t, []byte(`[{"string_list_data": `))
	if err == nil {
		t.Fatal("expected an error for truncated json")
	}
	if errors.Is(err, instagram.ErrNoFollowers) {
		t.Fatal("truncated json should report a parse failure, not an empty list")
	}
}

func TestParseIsDeterministicallyOrdered(t *testing.T) {
	body := fixture(t, "followers_array.json")
	first, err := parse(t, body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	second, err := parse(t, body)
	if err != nil {
		t.Fatalf("parse again: %v", err)
	}

	for i := range first {
		if first[i].Username != second[i].Username {
			t.Fatalf("ordering is not stable: %v vs %v", names(first), names(second))
		}
	}
}
