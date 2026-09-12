// Package web holds the embedded templates and static assets for the UI.
//
// Everything is compiled into the binary, so the container needs no asset
// directory and the interface works without any outside network access.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

//go:embed templates/*.html static/*
var assets embed.FS

// pages maps a page template to the layout it is rendered inside.
var pages = []string{"index.html", "diff.html", "dashboard.html"}

// Templates parses every page against the shared layout. Each page is its own
// template set, so they can all define the same content block.
func Templates() (map[string]*template.Template, error) {
	out := make(map[string]*template.Template, len(pages))

	for _, page := range pages {
		tmpl, err := template.New(page).Funcs(funcs()).ParseFS(assets,
			"templates/layout.html", "templates/"+page)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", page, err)
		}
		out[page] = tmpl
	}
	return out, nil
}

// AssetVersion is a short hash of every embedded static file, computed once at
// startup. Templates append it to asset URLs so that a new build is a new URL.
//
// Without it a released fix to the stylesheet or the script simply does not
// arrive: the assets are embedded, so their modification time is the zero time,
// http.ServeContent then omits Last-Modified, and with no validator of any kind
// a browser keeps serving whatever it cached the first time. That shipped once,
// and the symptom was a user running new HTML against an old stylesheet with no
// reason to suspect their browser rather than the release.
var AssetVersion = assetVersion()

// assetVersion hashes the static files, names and contents, in the order the
// embedded filesystem walks them, which is deterministic.
func assetVersion() string {
	sum := sha256.New()

	err := fs.WalkDir(assets, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		f, err := assets.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		// A hash.Hash never reports a write error, by its own contract.
		_, _ = fmt.Fprintf(sum, "%s\x00", path)

		_, err = io.Copy(sum, f)
		return err
	})
	if err != nil {
		// The assets are compiled in, so this cannot fail for any reason a
		// caller could act on. Fall back to a constant: asset URLs stop being
		// versioned, which is the behaviour before this existed.
		return "dev"
	}

	return hex.EncodeToString(sum.Sum(nil))[:12]
}

// StaticHandler serves the embedded stylesheet and script.
//
// A request carrying the current version may be cached forever, because a
// changed file arrives under a different URL. Anything else must be revalidated,
// so a stale bookmark or a hand-typed path cannot pin an old asset.
func StaticHandler() (http.Handler, error) {
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, fmt.Errorf("open static assets: %w", err)
	}

	files := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") == AssetVersion {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		// An entity tag gives the browser something to revalidate against,
		// which the embedded filesystem's zero modification time does not.
		w.Header().Set("ETag", `"`+AssetVersion+`"`)

		if match := r.Header.Get("If-None-Match"); match != "" {
			for _, tag := range strings.Split(match, ",") {
				if strings.Trim(strings.TrimSpace(tag), `"`) == AssetVersion {
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}

		files.ServeHTTP(w, r)
	}), nil
}

func funcs() template.FuncMap {
	return template.FuncMap{
		// timestamp renders a time in a compact, sortable form. Times are
		// stored in UTC; the browser rewrites them to local time.
		"timestamp": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.UTC().Format(time.RFC3339)
		},
		"shortDate": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.UTC().Format("2006-01-02")
		},
		"shortTime": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.UTC().Format("2006-01-02 15:04")
		},
		"signed": func(n int) string {
			if n == 0 {
				return "0"
			}
			return fmt.Sprintf("%+d", n)
		},
		// negated renders a removal count as a negative number, without
		// producing the nonsensical "-0".
		"negated": func(n int) string {
			if n == 0 {
				return "0"
			}
			return fmt.Sprintf("-%d", n)
		},
		"profileURL": func(username string) template.URL {
			return template.URL("https://www.instagram.com/" + template.URLQueryEscaper(username))
		},
		"deref": func(v *int64) string {
			if v == nil {
				return "—"
			}
			return fmt.Sprintf("%d", *v)
		},
		"add": func(a, b float64) float64 { return a + b },
		"sub": func(a, b float64) float64 { return a - b },
		"plural": func(n int, one, many string) string {
			if n == 1 {
				return one
			}
			return many
		},
		// dict builds an ad-hoc map so a shared sub-template can be reused with
		// different arguments.
		"dict": func(pairs ...any) (map[string]any, error) {
			if len(pairs)%2 != 0 {
				return nil, fmt.Errorf("dict expects key/value pairs, got %d arguments", len(pairs))
			}
			out := make(map[string]any, len(pairs)/2)
			for i := 0; i < len(pairs); i += 2 {
				key, ok := pairs[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict key %d is %T, want string", i, pairs[i])
				}
				out[key] = pairs[i+1]
			}
			return out, nil
		},
	}
}
