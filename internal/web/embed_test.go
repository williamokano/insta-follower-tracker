package web

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// staticRef finds every reference to an embedded asset in the templates.
var staticRef = regexp.MustCompile(`/static/[A-Za-z0-9._/-]+(\?[^"']*)?`)

// TestAssetReferencesCarryTheVersion guards the one thing that makes a released
// UI change actually arrive. The assets are embedded, so they have no
// modification time and http.ServeContent sends no validator; a browser that
// cached them once will keep using that copy against newly rendered HTML unless
// the URL itself changes. An unversioned reference here is invisible in
// development, where the cache is empty, and silently strands every existing
// user on the previous stylesheet.
func TestAssetReferencesCarryTheVersion(t *testing.T) {
	entries, err := assets.ReadDir("templates")
	if err != nil {
		t.Fatalf("read templates: %v", err)
	}

	checked := 0
	for _, entry := range entries {
		body, err := assets.ReadFile("templates/" + entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}

		for _, ref := range staticRef.FindAllString(string(body), -1) {
			checked++
			if !strings.Contains(ref, "?v={{.AssetVersion}}") {
				t.Errorf("%s references %q without ?v={{.AssetVersion}}; "+
					"browsers will keep serving the copy they already cached",
					entry.Name(), ref)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no asset references found; the regex or the templates moved")
	}
}

func TestAssetVersionIsDerivedFromContent(t *testing.T) {
	if AssetVersion == "" || AssetVersion == "dev" {
		t.Fatalf("AssetVersion = %q, want a hash of the embedded files", AssetVersion)
	}
	if got := assetVersion(); got != AssetVersion {
		t.Fatalf("assetVersion() = %q on a second call, want the stable %q", got, AssetVersion)
	}
}

// TestStaticHandlerCaching pins both halves of the bargain: a URL carrying the
// current version may be kept forever, and anything else must be revalidated.
func TestStaticHandlerCaching(t *testing.T) {
	handler, err := StaticHandler()
	if err != nil {
		t.Fatalf("StaticHandler: %v", err)
	}

	tests := []struct {
		name      string
		target    string
		ifNoneMat string
		wantCode  int
		wantCache string
	}{
		{
			name:      "current version is immutable",
			target:    "/app.css?v=" + AssetVersion,
			wantCode:  http.StatusOK,
			wantCache: "public, max-age=31536000, immutable",
		},
		{
			name:      "unversioned must be revalidated",
			target:    "/app.css",
			wantCode:  http.StatusOK,
			wantCache: "no-cache",
		},
		{
			name:      "stale version must be revalidated",
			target:    "/app.css?v=0000deadbeef",
			wantCode:  http.StatusOK,
			wantCache: "no-cache",
		},
		{
			name:      "matching entity tag is not resent",
			target:    "/app.css",
			ifNoneMat: `"` + AssetVersion + `"`,
			wantCode:  http.StatusNotModified,
			wantCache: "no-cache",
		},
		{
			name:      "outdated entity tag is resent",
			target:    "/app.css",
			ifNoneMat: `"0000deadbeef"`,
			wantCode:  http.StatusOK,
			wantCache: "no-cache",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			if tt.ifNoneMat != "" {
				req.Header.Set("If-None-Match", tt.ifNoneMat)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.wantCache {
				t.Errorf("Cache-Control = %q, want %q", got, tt.wantCache)
			}
			if got := rec.Header().Get("ETag"); got != `"`+AssetVersion+`"` {
				t.Errorf("ETag = %q, want the asset version", got)
			}
		})
	}
}
