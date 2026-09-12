package instagram

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"time"
)

// startHerePattern matches the export's own summary page, which newer
// downloads use to state the date range they cover.
var startHerePattern = regexp.MustCompile(`(?i)(^|/)start_here\.html?$`)

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

	// Entries are grouped by the list they belong to. A large list is split
	// across numbered parts, so several entries can feed one kind.
	parts := map[ListKind][]*zip.File{}
	var startHere *zip.File
	var newestEntry time.Time

	inspected := 0
	for _, f := range zr.File {
		inspected++
		if inspected > MaxArchiveEntries {
			return nil, fmt.Errorf("archive contains more than %d entries", MaxArchiveEntries)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if mod := f.Modified.UTC(); mod.After(newestEntry) {
			newestEntry = mod
		}
		if startHerePattern.MatchString(f.Name) {
			startHere = f
			continue
		}
		if kind, ok := kindForEntry(f.Name); ok {
			parts[kind] = append(parts[kind], f)
		}
	}

	budget := int64(MaxDecompressedBytes)
	lists := map[ListKind][]Follower{}

	for _, d := range listDefinitions {
		files := parts[d.Kind]
		if len(files) == 0 {
			continue
		}
		// followers_1 before followers_2, so parts merge predictably.
		sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

		var members []Follower
		parsedAny := false
		for _, f := range files {
			body, used, err := readArchiveEntry(f, budget)
			if err != nil {
				return nil, err
			}
			budget -= used

			followers, recognised, err := parseDocument(body)
			if err != nil || !recognised {
				// One unreadable part should not discard the others.
				continue
			}
			parsedAny = true
			members = append(members, followers...)
		}
		// A recognised but empty list is recorded as empty. Having no pending
		// requests is a fact about the account; leaving the list out would
		// instead say nothing was known, and the diff would skip it.
		if parsedAny {
			lists[d.Kind] = dedupe(members)
		}
	}

	if len(lists[ListFollowers]) == 0 {
		return nil, &ArchiveContentsError{Entries: inspected, NearMisses: nearMissNames(zr.File)}
	}

	export := &Export{Lists: lists, Coverage: archiveCoverage(startHere, budget)}
	export.TakenAt, export.TakenAtSource = archiveTakenAt(startHere, newestEntry, budget)
	if startHere != nil {
		if body, _, err := readArchiveEntry(startHere, budget); err == nil {
			export.Owner, _ = ownerFromMetadata(body)
		}
	}
	return export, nil
}

// OwnerFromArchive reads just the account an export belongs to, without parsing
// any of the lists.
//
// Intake needs the account before it can file an upload, and parsing is the
// worker's job, so this reads the one small summary page and nothing else.
func OwnerFromArchive(r io.ReaderAt, size int64) (string, bool) {
	if size <= 0 {
		return "", false
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", false
	}

	inspected := 0
	for _, f := range zr.File {
		inspected++
		if inspected > MaxArchiveEntries {
			return "", false
		}
		if f.FileInfo().IsDir() || !startHerePattern.MatchString(f.Name) {
			continue
		}
		body, _, err := readArchiveEntry(f, MaxJSONBytes)
		if err != nil {
			return "", false
		}
		return ownerFromMetadata(body)
	}
	return "", false
}

// nearMissNames lists the relationship lists an archive did hold, so a missing
// follower list explains itself.
func nearMissNames(files []*zip.File) []string {
	out := make([]string, 0, 4)
	for _, f := range files {
		if len(out) >= 4 || f.FileInfo().IsDir() {
			continue
		}
		if kind, ok := kindForEntry(f.Name); ok && kind != ListFollowers {
			out = append(out, path.Base(f.Name))
		}
	}
	return out
}

// archiveTakenAt establishes when an export was generated.
//
// What the export declares is preferred, being an explicit statement rather
// than a file attribute. Older downloads say nothing, so the archive's own
// timestamps carry those; on real exports the two agreed to the minute.
func archiveTakenAt(startHere *zip.File, newestEntry time.Time, budget int64) (time.Time, string) {
	if startHere != nil {
		if body, _, err := readArchiveEntry(startHere, budget); err == nil {
			if t, ok := takenAtFromMetadata(body); ok {
				return t, SourceDeclared
			}
		}
	}
	if !newestEntry.IsZero() {
		return newestEntry, SourceArchive
	}
	return time.Time{}, ""
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
