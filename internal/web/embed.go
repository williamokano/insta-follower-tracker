// Package web holds the embedded templates and static assets for the UI.
//
// Everything is compiled into the binary, so the container needs no asset
// directory and the interface works without any outside network access.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"time"
)

//go:embed templates/*.html static/*
var assets embed.FS

// pages maps a page template to the layout it is rendered inside.
var pages = []string{"index.html", "diff.html"}

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

// StaticHandler serves the embedded stylesheet and script.
func StaticHandler() (http.Handler, error) {
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, fmt.Errorf("open static assets: %w", err)
	}
	return http.FileServer(http.FS(sub)), nil
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
