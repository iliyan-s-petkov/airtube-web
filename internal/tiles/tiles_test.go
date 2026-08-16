package tiles_test

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"airbg.org/internal/tiles"
)

const origin = "https://airbg.org"

// archive is testdata's PMTiles filename. Dated, like the ones docs/tiles.md
// has the operator generate, and passed in rather than compiled into the
// package — which is the whole point of tiles.archive.
const archive = "bulgaria-20260815.pmtiles"

func handler(t *testing.T) http.Handler {
	t.Helper()
	h, err := tiles.NewHandler("testdata", archive, origin)
	if err != nil {
		t.Fatalf("NewHandler error = %v, want nil", err)
	}
	return h
}

// copyTestdata mirrors testdata/ into a temp directory, for the tests that need
// to add a file to it. Writing into the committed directory instead would make
// the suite mutate its own version-controlled inputs.
func copyTestdata(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir("testdata", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("testdata", p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
	if err != nil {
		t.Fatalf("copying testdata: %v", err)
	}
	return dst
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
		"/" + archive,
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
	// A copy in a temp directory, not a file written into the committed
	// testdata/: seeding and removing a file in a shared, version-controlled
	// directory races every other test reading it under -count=2 -parallel, and
	// a crashed run leaves the stray file behind in the working tree.
	dir := copyTestdata(t)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("private"), 0o600); err != nil {
		t.Fatalf("seeding notes.txt: %v", err)
	}

	h, err := tiles.NewHandler(dir, archive, origin)
	if err != nil {
		t.Fatalf("NewHandler error = %v, want nil", err)
	}
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
	resp := do(t, h, http.MethodGet, "/"+archive, http.Header{"Range": []string{"bytes=8-11"}})
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

// TestCORSHeadersOnRefusals pins the PLACEMENT of the header block, which
// nothing else does: TestCORSHeaders asserts on a 200, TestOnlyGetAndHead on a
// status and Allow, TestPathTraversal on a status alone. Moving the block below
// the method switch and the allowlist check left every one of them green.
//
// The headers belong on refusals for a concrete reason: without
// Access-Control-Allow-Origin the browser hands the page a network error
// instead of the 404 or 405, so MapLibre's error handler cannot report what
// went wrong and the operator debugs a blank map with no signal on either side.
// Vary: Origin belongs on them because a cache must not serve one origin's
// answer to another's.
//
// X-Content-Type-Options is asserted for completeness, not as a discriminator:
// net/http's own http.Error and http.NotFound set nosniff on the bodies they
// write, so that one header survives the moved-block mutation. The CORS headers
// and Vary are what actually pin the placement.
func TestCORSHeadersOnRefusals(t *testing.T) {
	h := handler(t)
	for _, tc := range []struct {
		name   string
		method string
		target string
		status int
	}{
		{"404", http.MethodGet, "/no-such-file.txt", http.StatusNotFound},
		{"404 traversal", http.MethodGet, "/glyphs/../../style.json", http.StatusNotFound},
		{"405", http.MethodPost, "/style.json", http.StatusMethodNotAllowed},
		{"405 on a path that does not exist", http.MethodDelete, "/nope", http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := do(t, h, tc.method, tc.target, nil)
			if resp.StatusCode != tc.status {
				t.Fatalf("%s %s = %d, want %d", tc.method, tc.target, resp.StatusCode, tc.status)
			}
			for header, want := range map[string]string{
				"Access-Control-Allow-Origin": origin,
				"X-Content-Type-Options":      "nosniff",
				"Vary":                        "Origin",
			} {
				if got := resp.Header.Get(header); got != want {
					t.Errorf("%s %s (%d): %s = %q, want %q",
						tc.method, tc.target, resp.StatusCode, header, got, want)
				}
			}
		})
	}
}

// TestVaryOriginOnASuccess. Vary was asserted nowhere at all; a cache that
// ignores it can serve one origin's response to another.
func TestVaryOriginOnASuccess(t *testing.T) {
	h := handler(t)
	if got := do(t, h, http.MethodGet, "/style.json", nil).Header.Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q", got, "Origin")
	}
}

func TestPreflight(t *testing.T) {
	h := handler(t)
	resp := do(t, h, http.MethodOptions, "/"+archive, http.Header{
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
	if _, err := tiles.NewHandler(dir, archive, origin); err == nil {
		t.Fatal("NewHandler on an empty directory returned nil error, want an error")
	}

	if err := os.WriteFile(filepath.Join(dir, "style.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := tiles.NewHandler(dir, archive, origin)
	if err == nil {
		t.Fatal("NewHandler with style.json but no archive returned nil error, want an error")
	}
	if got := fmt.Sprint(err); got == "" {
		t.Error("error message is empty")
	}
}

// TestServesTheConfiguredArchiveAndNoOther. Still exactly one archive name, as
// before — but the CONFIGURED one. docs/tiles.md has the operator generate
// bulgaria-YYYYMMDD.pmtiles and write that name into style.json; against a
// hardcoded allowlist the handler 404'd it, producing a blank map with no
// server-side error. The dated name is also what makes the year-long immutable
// Cache-Control honest, since a regenerated basemap gets a URL nobody has
// cached.
func TestServesTheConfiguredArchiveAndNoOther(t *testing.T) {
	dir := copyTestdata(t)
	// A second archive in the same directory — exactly what a regeneration
	// leaves behind before the old file is swept up.
	other := "bulgaria-20250101.pmtiles"
	if err := os.WriteFile(filepath.Join(dir, other), []byte("PMTilesSTALESTALE0123456"), 0o600); err != nil {
		t.Fatal(err)
	}

	h, err := tiles.NewHandler(dir, archive, origin)
	if err != nil {
		t.Fatalf("NewHandler error = %v, want nil", err)
	}
	if got := do(t, h, http.MethodGet, "/"+archive, nil).StatusCode; got != http.StatusOK {
		t.Errorf("GET /%s = %d, want 200", archive, got)
	}
	if got := do(t, h, http.MethodGet, "/"+other, nil).StatusCode; got != http.StatusNotFound {
		t.Errorf("GET /%s = %d, want 404: only the configured archive is served", other, got)
	}

	// The converse, so this cannot be satisfied by an allowlist that happens to
	// accept any *.pmtiles: point the config at the other file and the two
	// answers must swap.
	h2, err := tiles.NewHandler(dir, other, origin)
	if err != nil {
		t.Fatalf("NewHandler(%q) error = %v, want nil", other, err)
	}
	if got := do(t, h2, http.MethodGet, "/"+other, nil).StatusCode; got != http.StatusOK {
		t.Errorf("with tiles.archive = %q, GET /%s = %d, want 200", other, other, got)
	}
	if got := do(t, h2, http.MethodGet, "/"+archive, nil).StatusCode; got != http.StatusNotFound {
		t.Errorf("with tiles.archive = %q, GET /%s = %d, want 404", other, archive, got)
	}
}

// TestConstructorRejectsAnArchiveThatIsNotThere. A mis-set tiles.archive must
// be a startup failure for the same reason a mis-set tiles.dir is: the runtime
// symptom is a blank map with nothing on the server to say why, and that is the
// whole reason this constructor validates at all.
func TestConstructorRejectsAnArchiveThatIsNotThere(t *testing.T) {
	dir := copyTestdata(t)
	if _, err := tiles.NewHandler(dir, "bulgaria-19990101.pmtiles", origin); err == nil {
		t.Fatal("NewHandler with an archive name that names no file returned nil error, want an error")
	}
}

// TestConstructorRejectsANonPlainArchiveName. os.DirFS already bounds every
// read to dir, so this is defence in depth and a clearer error rather than the
// control that stops traversal.
func TestConstructorRejectsANonPlainArchiveName(t *testing.T) {
	for _, name := range []string{
		"glyphs/../style.json",
		"../style.json",
		`sub\bulgaria.pmtiles`,
		".",
		"..",
	} {
		if _, err := tiles.NewHandler("testdata", name, origin); err == nil {
			t.Errorf("NewHandler with archive = %q returned nil error, want an error", name)
		}
	}
}

func TestConstructorRejectsEmptyArguments(t *testing.T) {
	if _, err := tiles.NewHandler("testdata", "", origin); err == nil {
		t.Error("NewHandler with an empty archive returned nil error, want an error")
	}
	if _, err := tiles.NewHandler("", archive, origin); err == nil {
		t.Error("NewHandler with an empty dir returned nil error, want an error")
	}
	if _, err := tiles.NewHandler("testdata", archive, ""); err == nil {
		t.Error("NewHandler with an empty allowOrigin returned nil error, want an error")
	}
}
