package tiles_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"airbg.org/internal/tiles"
)

const origin = "https://airbg.org"

func handler(t *testing.T) http.Handler {
	t.Helper()
	h, err := tiles.NewHandler("testdata", origin)
	if err != nil {
		t.Fatalf("NewHandler error = %v, want nil", err)
	}
	return h
}

func do(t *testing.T, h http.Handler, method, target string, hdr http.Header) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestServesTheThreeArtefacts(t *testing.T) {
	h := handler(t)
	for _, path := range []string{
		"/style.json",
		"/bulgaria.pmtiles",
		"/glyphs/NotoSans-Regular/0-255.pbf",
	} {
		resp := do(t, h, http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
		}
	}
}

// TestRejectsAnythingOutsideTheAllowlist. The directory is an operator-provided
// path; whatever else lands in it — a build log, a half-written temp file, the
// planetiler jar — must not become a public download.
func TestRejectsAnythingOutsideTheAllowlist(t *testing.T) {
	if err := os.WriteFile(filepath.Join("testdata", "notes.txt"), []byte("private"), 0o600); err != nil {
		t.Fatalf("seeding notes.txt: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join("testdata", "notes.txt")) })

	h := handler(t)
	if got := do(t, h, http.MethodGet, "/notes.txt", nil).StatusCode; got != http.StatusNotFound {
		t.Errorf("GET /notes.txt = %d, want 404", got)
	}
}

// TestPathTraversal. os.DirFS closes this structurally rather than by
// validating input, and the allowlist closes it a second time. Mutate by
// swapping os.DirFS for http.Dir AND widening the allowlist to see it fail.
func TestPathTraversal(t *testing.T) {
	h := handler(t)
	for _, target := range []string{
		"/../../etc/passwd",
		"/..%2f..%2fetc%2fpasswd",
		"/glyphs/../../style.json",
		"//etc/passwd",
		"/glyphs/../0-255.pbf",
		"/glyphs/./0-255.pbf",
	} {
		resp := do(t, h, http.MethodGet, target, nil)
		if resp.StatusCode == http.StatusOK {
			t.Errorf("GET %s = 200, want a refusal", target)
		}
	}
}

func TestOnlyGetAndHead(t *testing.T) {
	h := handler(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		resp := do(t, h, method, "/style.json", nil)
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s /style.json = %d, want 405", method, resp.StatusCode)
		}
		if got := resp.Header.Get("Allow"); got != "GET, HEAD" {
			t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
		}
	}
	if got := do(t, h, http.MethodHead, "/style.json", nil).StatusCode; got != http.StatusOK {
		t.Errorf("HEAD /style.json = %d, want 200", got)
	}
}

// TestRangeRequest. Range support is not a nicety here: the PMTiles protocol
// reads a 300 MB archive exclusively through ranges. A handler that answered
// 200 with the whole body would send the entire file on the first tile.
func TestRangeRequest(t *testing.T) {
	h := handler(t)
	resp := do(t, h, http.MethodGet, "/bulgaria.pmtiles", http.Header{"Range": []string{"bytes=8-11"}})
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "FAKE" {
		t.Errorf("body = %q, want %q", body, "FAKE")
	}
	if got, want := resp.Header.Get("Content-Range"), "bytes 8-11/25"; got != want {
		t.Errorf("Content-Range = %q, want %q", got, want)
	}
}

// TestCORSHeaders. Tiles live on a different host from the application, so
// every fetch is cross-origin. Missing headers here produce a blank map with no
// server-side error — indistinguishable from a tile-generation mistake.
func TestCORSHeaders(t *testing.T) {
	h := handler(t)
	resp := do(t, h, http.MethodGet, "/style.json", nil)
	for header, want := range map[string]string{
		"Access-Control-Allow-Origin":   origin,
		"Access-Control-Allow-Headers":  "Range",
		"Access-Control-Expose-Headers": "Content-Range",
		"Cache-Control":                 "public, max-age=31536000, immutable",
		"X-Content-Type-Options":        "nosniff",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got == "*" {
		t.Error(`Access-Control-Allow-Origin is "*"; it must name the application origin`)
	}
}

func TestPreflight(t *testing.T) {
	h := handler(t)
	resp := do(t, h, http.MethodOptions, "/bulgaria.pmtiles", http.Header{
		"Origin":                        []string{origin},
		"Access-Control-Request-Method": []string{"GET"},
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("OPTIONS = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("preflight Access-Control-Allow-Origin = %q, want %q", got, origin)
	}
}

// TestConstructorRejectsAnIncompleteDirectory. A mis-set tiles.dir must fail at
// startup. Discovering it from a blank map in production is the outcome this
// guards against.
func TestConstructorRejectsAnIncompleteDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := tiles.NewHandler(dir, origin); err == nil {
		t.Fatal("NewHandler on an empty directory returned nil error, want an error")
	}

	if err := os.WriteFile(filepath.Join(dir, "style.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := tiles.NewHandler(dir, origin)
	if err == nil {
		t.Fatal("NewHandler with style.json but no bulgaria.pmtiles returned nil error, want an error")
	}
	if got := fmt.Sprint(err); got == "" {
		t.Error("error message is empty")
	}
}

func TestConstructorRejectsEmptyArguments(t *testing.T) {
	if _, err := tiles.NewHandler("", origin); err == nil {
		t.Error("NewHandler with an empty dir returned nil error, want an error")
	}
	if _, err := tiles.NewHandler("testdata", ""); err == nil {
		t.Error("NewHandler with an empty allowOrigin returned nil error, want an error")
	}
}
