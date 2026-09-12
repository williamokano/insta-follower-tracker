package instagram

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// handlePattern is the set of characters Instagram allows in a username.
var handlePattern = regexp.MustCompile(`^[a-z0-9._]{1,30}$`)

// reservedPaths are first path segments on instagram.com that are site
// features rather than profiles. The export's own chrome links to several of
// them, so they must not be mistaken for followers.
var reservedPaths = map[string]bool{
	"about": true, "accounts": true, "api": true, "challenge": true,
	"developer": true, "direct": true, "explore": true, "emails": true,
	"help": true, "legal": true, "p": true, "press": true, "privacy": true,
	"reel": true, "reels": true, "session": true, "stories": true,
	"terms": true, "tv": true,
}

// looksLikeHTML reports whether a document is markup rather than JSON.
func looksLikeHTML(body []byte) bool {
	head := bytes.TrimLeft(body, " \t\r\n\uFEFF")
	if len(head) == 0 || head[0] != '<' {
		return false
	}
	// Guard against an XML document that merely starts with a bracket.
	lower := bytes.ToLower(head[:min(len(head), 512)])
	return bytes.Contains(lower, []byte("<html")) ||
		bytes.Contains(lower, []byte("<!doctype html")) ||
		bytes.Contains(lower, []byte("<div")) ||
		bytes.Contains(lower, []byte("<a "))
}

// parseHTMLExport reads a follower list out of Instagram's HTML export.
//
// It keys on the profile links themselves rather than on the surrounding
// markup. The export's CSS class names are generated and change without
// notice, but a follower is always rendered as a link to their profile, so
// matching on the href survives cosmetic redesigns.
//
// Follow timestamps are deliberately not read. The HTML export renders them as
// localised prose ("1 de janeiro de 2024"), so parsing them would be a guess at
// the exporting account's language. They are only ever display metadata, and
// the JSON export carries them properly.
func parseHTMLExport(body []byte) ([]Follower, bool, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("parse html export: %w", err)
	}

	var out []Follower
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if !strings.EqualFold(attr.Key, "href") {
					continue
				}
				if username := usernameFromProfileURL(attr.Val); username != "" {
					out = append(out, Follower{Username: username, Href: attr.Val})
				}
				break
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)

	// A list page with no profile links is an empty list, which for an
	// auxiliary list is ordinary. Whether an empty list is acceptable is
	// decided by the caller, which knows which list it is reading.
	return out, true, nil
}

// usernameFromProfileURL extracts a handle from an Instagram profile link,
// returning an empty string for anything that is not one.
func usernameFromProfileURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}

	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")
	if host != "instagram.com" {
		return ""
	}

	// A profile is a single path segment. Anything deeper is a post, reel or
	// story rather than an account.
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) != 1 || segments[0] == "" {
		return ""
	}

	handle := normalizeUsername(segments[0])
	if reservedPaths[handle] || !handlePattern.MatchString(handle) {
		return ""
	}
	return handle
}
