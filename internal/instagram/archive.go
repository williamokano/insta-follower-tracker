package instagram

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
)

// startHerePattern matches the export's own summary page, which newer
// downloads use to state the date range they cover.
var startHerePattern = regexp.MustCompile(`(?i)(^|/)start_here\.html?$`)

// relatedListPattern matches the other relationship lists that sit beside the
// follower list, so a failure can point at what was there instead.
var relatedListPattern = regexp.MustCompile(
	`(?i)(^|/)(following|following_hashtags|close_friends|pending_follow_requests|recent_follow_requests|` +
		`recently_unfollowed_(profiles|accounts)|blocked_(profiles|accounts)|restricted_profiles|` +
		`removed_suggestions|profiles_you've_favorited)\.(json|html?)$`)

// parseArchive walks an export archive and merges every follower list it finds.
//
// Only entries whose path matches followersFilePattern are read; the rest of
// the export (media, messages, and everything else) is skipped without being
// decompressed. Nothing is ever extracted to disk, so a hostile path inside the
// archive has nowhere to escape to.
func parseArchive(r io.ReaderAt, size int64) (*Export, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}

	names := make([]string, 0, len(zr.File))
	byName := make(map[string]*zip.File, len(zr.File))
	nearMisses := make([]string, 0, 4)
	var startHere *zip.File

	inspected := 0
	for _, f := range zr.File {
		inspected++
		if inspected > MaxArchiveEntries {
			return nil, fmt.Errorf("archive contains more than %d entries", MaxArchiveEntries)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if startHerePattern.MatchString(f.Name) {
			startHere = f
		}
		if !followersFilePattern.MatchString(f.Name) {
			if len(nearMisses) < 4 && relatedListPattern.MatchString(f.Name) {
				nearMisses = append(nearMisses, path.Base(f.Name))
			}
			continue
		}
		names = append(names, f.Name)
		byName[f.Name] = f
	}

	if len(names) == 0 {
		return nil, &ArchiveContentsError{Entries: inspected, NearMisses: nearMisses}
	}
	// followers_1.json before followers_2.json, so parts merge predictably.
	sort.Strings(names)

	var (
		all       []Follower
		budget    = int64(MaxDecompressedBytes)
		anyParsed bool
	)
	for _, name := range names {
		body, used, err := readArchiveEntry(byName[name], budget)
		if err != nil {
			return nil, err
		}
		budget -= used

		followers, err := parseDocument(body)
		if err != nil {
			// A single unreadable part should not discard the others; only
			// report failure if nothing at all could be parsed.
			continue
		}
		anyParsed = true
		all = append(all, followers...)
	}

	if !anyParsed || len(all) == 0 {
		return nil, ErrNoFollowers
	}
	all = dedupe(all)

	return &Export{Followers: all, Coverage: archiveCoverage(startHere, budget)}, nil
}

// archiveCoverage reads the range the export declares about itself, when it
// carries one. Older downloads state nothing, and report nothing.
func archiveCoverage(startHere *zip.File, budget int64) Coverage {
	if startHere == nil {
		return Coverage{}
	}
	body, _, err := readArchiveEntry(startHere, budget)
	if err != nil {
		return Coverage{}
	}
	return coverageFromMetadata(body)
}

func readArchiveEntry(f *zip.File, budget int64) ([]byte, int64, error) {
	if budget <= 0 {
		return nil, 0, fmt.Errorf("archive expands beyond the %d byte limit", int64(MaxDecompressedBytes))
	}

	rc, err := f.Open()
	if err != nil {
		return nil, 0, fmt.Errorf("open %s: %w", f.Name, err)
	}
	defer rc.Close()

	body, err := io.ReadAll(io.LimitReader(rc, budget+1))
	if err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", f.Name, err)
	}
	if int64(len(body)) > budget {
		return nil, 0, fmt.Errorf("archive expands beyond the %d byte limit", int64(MaxDecompressedBytes))
	}
	return body, int64(len(body)), nil
}
