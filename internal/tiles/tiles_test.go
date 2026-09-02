package tiles_test

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"airbg.org/internal/tiles"
)

const origin = "https://airbg.org"

// kitOrigin stands for the design kit, the second origin the allowlist exists
// for. Deliberately not a subdomain of origin: a substring test would pass one
// of those by accident.
const kitOrigin = "https://kit.example"

// archive is testdata's PMTiles filename. Dated, like the ones docs/tiles.md
// has the operator generate, and passed in rather than compiled into the
// package — which is the whole point of tiles.archive.
const archive = "bulgaria-20260815.pmtiles"

func handler(t *testing.T) http.Handler {
	t.Helper()
	h, err := tiles.NewHandler("testdata", archive, []string{origin, kitOrigin})
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

// do issues one request. It supplies Origin when the caller does not, because
// every request this handler exists to answer is a cross-origin one — the tiles
// are on their own host — and the CORS headers are now conditional on that
// header being present and allowed. The tests that care which origin asked
// pass their own; see TestCORSAllowlist.
func do(t *testing.T, h http.Handler, method, target string, hdr http.Header) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	// Presence, not value: TestCORSAllowlist sets Origin to "" deliberately to
	// exercise the no-origin case, and a Get()-based test would overwrite it.
	if _, ok := req.Header["Origin"]; !ok {
		req.Header.Set("Origin", origin)
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

// TestCacheLifetimeMatchesHowEachArtefactIsAddressed.
//
// `immutable` is a promise that the bytes at a URL will never change, and it is
// unrevocable: a browser holding one does not ask again, so no server-side
// change can reach it. It is therefore only ever true of a URL that changes
// when its content does. Of the three artefacts, exactly one is like that.
//
//   - The archive is addressed by its dated, configured name, so a regeneration
//     produces a URL nobody has cached. Immutable is honest, and load-bearing:
//     without it every visitor re-fetches hundreds of megabytes of ranges.
//   - style.json has a FIXED name and is the artefact a regeneration rewrites —
//     it is what points at the new archive name. Marked immutable, a returning
//     visitor keeps a style document naming an archive the handler no longer
//     serves (the allowlist admits only the configured name), so the map is
//     blank for up to a year with no way to clear it. That is why this test
//     exists: the bug is invisible at deploy time and surfaces days later, on
//     other people's machines.
//   - Glyph paths are fixed too, so a font rebuild reuses them. Long, but not
//     immutable: a day bounds how long a stale atlas can outlive its rebuild.
//
// Asserted as exact strings rather than "contains immutable", so that widening
// any of the three has to be a deliberate edit to this table.
func TestCacheLifetimeMatchesHowEachArtefactIsAddressed(t *testing.T) {
	h := handler(t)
	for path, want := range map[string]string{
		"/style.json":                        "public, max-age=300, must-revalidate",
		"/" + archive:                        "public, max-age=31536000, immutable",
		"/glyphs/NotoSans-Regular/0-255.pbf": "public, max-age=86400",
	} {
		resp := do(t, h, http.MethodGet, path, nil)
		if got := resp.Header.Get("Cache-Control"); got != want {
			t.Errorf("GET %s: Cache-Control = %q, want %q", path, got, want)
		}
	}

	// The discriminator: everything except the dated archive must be
	// revalidatable. A future edit that reinstates one header for all three
	// would satisfy any single-path assertion above depending on which value it
	// picked; this fails for both directions of that mistake.
	for _, path := range []string{"/style.json", "/glyphs/NotoSans-Regular/0-255.pbf"} {
		if got := do(t, h, http.MethodGet, path, nil).Header.Get("Cache-Control"); strings.Contains(got, "immutable") {
			t.Errorf("GET %s: Cache-Control = %q — only the dated archive name may be immutable", path, got)
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

	h, err := tiles.NewHandler(dir, archive, []string{origin})
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
		"Access-Control-Expose-Headers": "Content-Range, Content-Length",
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

// TestCORSAllowlist is the whole point of the list: each allowed origin gets
// its OWN origin echoed back, and anything else gets no header at all.
//
// Echoing the request's origin is not cosmetic. A browser compares ACAO to the
// Origin it sent, byte for byte, so a handler that always returned the first
// configured origin would work for that one host and fail for every other on
// the list — while passing any test that only checked the header is non-empty.
// The kitOrigin case is what separates those two implementations.
//
// The refusal case asserts ABSENCE, not an empty value: an empty
// Access-Control-Allow-Origin is a header the browser must parse and reject,
// and it invites a later edit to "fix" it by supplying a default. The absent
// header is the unambiguous no.
func TestCORSAllowlist(t *testing.T) {
	h := handler(t)
	for _, tc := range []struct {
		name     string
		origin   string
		wantACAO string
	}{
		{"the site itself", origin, origin},
		{"the second allowed origin", kitOrigin, kitOrigin},
		{"an origin that is not on the list", "https://example.invalid", ""},
		// A prefix of an allowed origin, and an allowed origin with something
		// appended. Both match under a strings.HasPrefix or Contains test and
		// neither may match here.
		{"a prefix of an allowed origin", "https://kit.exam", ""},
		{"an allowed origin with a suffix", kitOrigin + ".evil.test", ""},
		{"no Origin header at all", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hdr := http.Header{}
			if tc.origin != "" {
				hdr.Set("Origin", tc.origin)
			} else {
				// do() fills in a default Origin when none is set, which is
				// right for every other test and wrong for this one case.
				hdr.Set("Origin", "")
			}
			resp := do(t, h, http.MethodGet, "/style.json", hdr)

			if got := resp.Header.Get("Access-Control-Allow-Origin"); got != tc.wantACAO {
				t.Errorf("Origin %q: Access-Control-Allow-Origin = %q, want %q", tc.origin, got, tc.wantACAO)
			}
			if tc.wantACAO == "" {
				if _, ok := resp.Header["Access-Control-Allow-Origin"]; ok {
					t.Errorf("Origin %q: Access-Control-Allow-Origin is present; a refusal must omit it entirely", tc.origin)
				}
			}
			// Vary belongs on every response including the refusals, or a
			// shared cache replays one origin's answer to another and the
			// allowlist above stops meaning anything.
			if got := resp.Header.Get("Vary"); got != "Origin" {
				t.Errorf("Origin %q: Vary = %q, want %q", tc.origin, got, "Origin")
			}
			// The refusal is a CORS refusal, not a 403: the bytes are public,
			// and it is the browser that must decline to hand them to a page.
			if resp.StatusCode != http.StatusOK {
				t.Errorf("Origin %q: status = %d, want 200", tc.origin, resp.StatusCode)
			}
		})
	}
}

// TestNeverAllowsCredentials is a negative assertion, and the only kind that
// can hold: nothing sets this header today, so the test exists to stop it being
// added.
//
// Echoing the requesting origin is safe precisely because credentials are not
// allowed. Together they are the combination that turns an allowlist slip — one
// over-broad entry, one validation gap — from "the wrong site can read public
// map tiles" into "the wrong site can act as a logged-in visitor". The tiles
// listener holds no session and needs no cookie, so the header has no use here
// and its absence is worth pinning.
//
// Asserted on the preflight as well as the read: Allow-Credentials on an
// OPTIONS response is what a browser consults before sending the credentialled
// request at all, so a mutation that set it only there would be invisible to a
// GET-only check.
func TestNeverAllowsCredentials(t *testing.T) {
	h := handler(t)
	for _, tc := range []struct {
		name   string
		method string
		hdr    http.Header
	}{
		{"read", http.MethodGet, http.Header{"Origin": []string{origin}}},
		{"read from the second origin", http.MethodGet, http.Header{"Origin": []string{kitOrigin}}},
		{"preflight", http.MethodOptions, http.Header{
			"Origin":                         []string{kitOrigin},
			"Access-Control-Request-Method":  []string{"GET"},
			"Access-Control-Request-Headers": []string{"Range"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := do(t, h, tc.method, "/style.json", tc.hdr)
			if _, ok := resp.Header["Access-Control-Allow-Credentials"]; ok {
				t.Errorf("Access-Control-Allow-Credentials = %q; it must never be set — this handler holds no session, and with an echoed origin it would make an allowlist slip a credential leak",
					resp.Header.Get("Access-Control-Allow-Credentials"))
			}
		})
	}
}

// TestOneHandlerServesAllThreeArtefacts. MapLibre and PMTiles fetch three
// different things — the style, the glyph atlases and byte ranges of the
// archive — and all three must clear the same allowlist. If any were served by
// a second handler or a bare file server, the map would fail with a font error
// or a tile error rather than a CORS one, which reads as a different bug
// entirely and sends the reader looking at the wrong thing.
//
// server.go mounts this handler as the tiles listener's whole Handler, with no
// mux and nothing to fall through to, so proving it here proves it end to end.
func TestOneHandlerServesAllThreeArtefacts(t *testing.T) {
	h := handler(t)
	for _, path := range []string{
		"/style.json",
		"/" + archive,
		"/glyphs/NotoSans-Regular/0-255.pbf",
	} {
		t.Run(path, func(t *testing.T) {
			allowed := do(t, h, http.MethodGet, path, http.Header{"Origin": []string{kitOrigin}})
			if got := allowed.Header.Get("Access-Control-Allow-Origin"); got != kitOrigin {
				t.Errorf("GET %s from %q: Access-Control-Allow-Origin = %q, want %q", path, kitOrigin, got, kitOrigin)
			}
			refused := do(t, h, http.MethodGet, path, http.Header{"Origin": []string{"https://example.invalid"}})
			if _, ok := refused.Header["Access-Control-Allow-Origin"]; ok {
				t.Errorf("GET %s from an origin off the list: Access-Control-Allow-Origin is present, want absent", path)
			}
		})
	}
}

// TestRangeReadIsReadable. A range response the script receives but cannot
// measure is not one PMTiles can parse, so both headers have to be exposed.
func TestRangeReadIsReadable(t *testing.T) {
	h := handler(t)
	resp := do(t, h, http.MethodGet, "/"+archive, http.Header{
		"Origin": []string{kitOrigin},
		"Range":  []string{"bytes=0-15"},
	})
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("ranged GET %s = %d, want 206", archive, resp.StatusCode)
	}
	want := "Content-Range, Content-Length"
	if got := resp.Header.Get("Access-Control-Expose-Headers"); got != want {
		t.Errorf("Access-Control-Expose-Headers = %q, want %q", got, want)
	}
}

// TestConstructorRejectsAWildcardOrigin. "*" is not a wider allowlist, it is no
// allowlist, and it would survive config validation if it ever reached the
// handler by another route.
func TestConstructorRejectsAWildcardOrigin(t *testing.T) {
	for _, origins := range [][]string{{"*"}, {origin, "*"}, {origin, ""}} {
		if _, err := tiles.NewHandler("testdata", archive, origins); err == nil {
			t.Errorf("NewHandler with allowOrigins = %q returned nil error, want an error", origins)
		}
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
	if _, err := tiles.NewHandler(dir, archive, []string{origin}); err == nil {
		t.Fatal("NewHandler on an empty directory returned nil error, want an error")
	}

	if err := os.WriteFile(filepath.Join(dir, "style.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := tiles.NewHandler(dir, archive, []string{origin})
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

	h, err := tiles.NewHandler(dir, archive, []string{origin})
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
	h2, err := tiles.NewHandler(dir, other, []string{origin})
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
	if _, err := tiles.NewHandler(dir, "bulgaria-19990101.pmtiles", []string{origin}); err == nil {
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
		if _, err := tiles.NewHandler("testdata", name, []string{origin}); err == nil {
			t.Errorf("NewHandler with archive = %q returned nil error, want an error", name)
		}
	}
}

func TestConstructorRejectsEmptyArguments(t *testing.T) {
	if _, err := tiles.NewHandler("testdata", "", []string{origin}); err == nil {
		t.Error("NewHandler with an empty archive returned nil error, want an error")
	}
	if _, err := tiles.NewHandler("", archive, []string{origin}); err == nil {
		t.Error("NewHandler with an empty dir returned nil error, want an error")
	}
	if _, err := tiles.NewHandler("testdata", archive, nil); err == nil {
		t.Error("NewHandler with an empty allowOrigin returned nil error, want an error")
	}
}
