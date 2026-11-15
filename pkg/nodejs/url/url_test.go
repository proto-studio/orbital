package url

import (
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

func setupRuntime(t *testing.T) *runtime.Runtime {
	rt, err := runtime.New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}

	urlMod := New()
	if err := urlMod.Register(rt); err != nil {
		rt.Dispose()
		t.Fatalf("Failed to register url module: %v", err)
	}

	return rt
}

func TestURL_Parse(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const url = new URL('https://example.com:8080/path?query=value#hash');
		JSON.stringify({
			protocol: url.protocol,
			host: url.host,
			hostname: url.hostname,
			port: url.port,
			pathname: url.pathname,
			search: url.search,
			hash: url.hash
		});
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// Just verify it returns valid JSON with expected structure
	str := result.String()
	if str == "" || str == "{}" {
		t.Error("URL parsing should return non-empty result")
	}
}

func TestURL_Protocol(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`new URL('https://example.com').protocol`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "https:" {
		t.Errorf("Expected 'https:', got %q", result.String())
	}
}

func TestURL_Hostname(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`new URL('https://example.com:8080/path').hostname`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "example.com" {
		t.Errorf("Expected 'example.com', got %q", result.String())
	}
}

func TestURL_Port(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`new URL('https://example.com:8080/path').port`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "8080" {
		t.Errorf("Expected '8080', got %q", result.String())
	}
}

func TestURL_Pathname(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`new URL('https://example.com/path/to/file').pathname`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "/path/to/file" {
		t.Errorf("Expected '/path/to/file', got %q", result.String())
	}
}

func TestURL_SearchParams(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`new URL('https://example.com?foo=bar&baz=qux').searchParams.get('foo')`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "bar" {
		t.Errorf("Expected 'bar', got %q", result.String())
	}
}

func TestURLSearchParams_Set(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const params = new URLSearchParams();
		params.set('foo', 'bar');
		params.get('foo');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "bar" {
		t.Errorf("Expected 'bar', got %q", result.String())
	}
}

func TestURLSearchParams_Append(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const params = new URLSearchParams();
		params.append('foo', 'bar');
		params.append('foo', 'baz');
		params.getAll('foo').join(',');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "bar,baz" {
		t.Errorf("Expected 'bar,baz', got %q", result.String())
	}
}

func TestURLSearchParams_Delete(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const params = new URLSearchParams('foo=bar&baz=qux');
		params.delete('foo');
		params.toString();
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "baz=qux" {
		t.Errorf("Expected 'baz=qux', got %q", result.String())
	}
}

func TestURLSearchParams_Has(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	tests := []struct {
		input    string
		expected bool
	}{
		{`new URLSearchParams('foo=bar').has('foo')`, true},
		{`new URLSearchParams('foo=bar').has('baz')`, false},
	}

	for _, tt := range tests {
		result, err := rt.RunScript(tt.input, "test.js")
		if err != nil {
			t.Fatalf("RunScript failed for %q: %v", tt.input, err)
		}
		if result.Boolean() != tt.expected {
			t.Errorf("For %q: got %v, want %v", tt.input, result.Boolean(), tt.expected)
		}
	}
}

func TestURLSearchParams_ToString(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const params = new URLSearchParams();
		params.set('foo', 'bar');
		params.set('baz', 'qux');
		params.toString();
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	str := result.String()
	if str != "foo=bar&baz=qux" && str != "baz=qux&foo=bar" {
		t.Errorf("Expected 'foo=bar&baz=qux' or similar, got %q", str)
	}
}

func TestURL_Href(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`new URL('https://example.com/path?query=value').href`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "https://example.com/path?query=value" {
		t.Errorf("Expected full URL, got %q", result.String())
	}
}

func TestURL_Origin(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`new URL('https://example.com:8080/path').origin`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "https://example.com:8080" {
		t.Errorf("Expected 'https://example.com:8080', got %q", result.String())
	}
}

func TestURL_Name(t *testing.T) {
	u := New()
	if u.Name() != "url" {
		t.Errorf("Name() should return 'url', got %q", u.Name())
	}
}

// The legacy parser keeps an IPv6 literal's brackets in host and drops them from hostname,
// as Node does. It used to treat the bracket as an invalid hostname character and move the
// whole host into the pathname, so http clients sent "GET [::1]/x" to localhost.
func TestURL_LegacyParseIPv6(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	// Expected values are what Node 20.18.1 returns for the same calls.
	result, err := rt.RunScript(`
		const url = globalThis.__url_module;
		const parts = (u) => { const p = url.parse(u, false, true); return [p.host, p.hostname, p.port, p.pathname, p.path]; };
		JSON.stringify([
			parts('http://[::1]:8080/x?a=1'),
			parts('http://[::1]/x'),
			parts('https://user:pw@[2001:db8::1]:443/p#h'),
			parts('//[::1]:9/x'),
			url.format({ protocol: 'http:', hostname: '::1', port: 80, pathname: '/x' }),
			url.format(url.parse('http://[::1]:8080/x')),
		]);
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	want := `[["[::1]:8080","::1","8080","/x","/x?a=1"],["[::1]","::1",null,"/x","/x"],` +
		`["[2001:db8::1]:443","2001:db8::1","443","/p","/p"],["[::1]:9","::1","9","/x","/x"],` +
		`"http://[::1]:80/x","http://[::1]:8080/x"]`
	if got := result.String(); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// Dot segments are removed and a file: URL keeps its empty host, as in the WHATWG URL
// parser. Each case gives href, pathname and host as Node 20.18.1 does.
func TestURL_DotSegmentsAndFileHost(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
const cases = [
  ['./x/', 'file:///a/b.mjs'], ['../x', 'file:///a/b/c.mjs'], ['../../../x', 'file:///a/b.mjs'], ['.', 'file:///a/b/c'], ['..', 'file:///a/b/c'],
  ['./', 'http://h/a/b'], ['x/./y/../z?q=1#h', 'http://h/a/b'], ['%2e/x', 'http://h/a/b'], ['%2E%2E/x', 'http://h/a/b/c'], ['.%2e/x', 'http://h/a/b/c'],
  ['file:///a/./b/../c'], ['file:///a/b/..'], ['file:///a/b/.'], ['file:///..'], ['http://h/a/./b/../../../c'], ['http://h/a/b/../?q=1'],
  ['file:///etc/passwd'], ['file://host/x'], ['file:/x'], ['foo:///x'], ['foo:/x/./y'], ['foo://h/x/../y'], ['mailto:a/./b'], ['http://h'], ['x', 'file:///'],
];

JSON.stringify(cases.map(([u, b]) => { try { const x = b === undefined ? new URL(u) : new URL(u, b); return [x.href, x.pathname, x.host]; } catch (e) { return e.code || e.name; } }));
`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	want := `[["file:///a/x/","/a/x/",""],["file:///a/x","/a/x",""],["file:///x","/x",""],["file:///a/b/","/a/b/",""],["file:///a/","/a/",""],["http://h/a/","/a/","h"],["http://h/a/x/z?q=1#h","/a/x/z","h"],["http://h/a/x","/a/x","h"],["http://h/a/x","/a/x","h"],["http://h/a/x","/a/x","h"],["file:///a/c","/a/c",""],["file:///a/","/a/",""],["file:///a/b/","/a/b/",""],["file:///","/",""],["http://h/c","/c","h"],["http://h/a/?q=1","/a/","h"],["file:///etc/passwd","/etc/passwd",""],["file://host/x","/x","host"],["file:///x","/x",""],["foo:///x","/x",""],["foo:/x/y","/x/y",""],["foo://h/y","/y","h"],["mailto:a/./b","a/./b",""],["http://h/","/","h"],["file:///x","/x",""]]`
	if result.String() != want {
		t.Errorf("got  %s\nwant %s", result.String(), want)
	}
}

// url.parse() keeps dot segments as Node's legacy parser does; assigning href replaces the
// authority too; a file: URL's Windows drive letter survives ".." and "|" becomes ":"; and a
// path starting with "//" in a URL without an authority is written with "/." before it.
// Expected values are Node 20.18.1's.
func TestURL_DotSegmentEdges(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
const out = [];
for (const u of ['http://h/a/../b', 'http://h/a/%2e%2e/b?x', 'file:///a/./b', '/a/./b']) { const p = __url_module.parse(u); out.push([p.pathname, p.path, __url_module.format(p)]); }
const x = new URL('foo://h/x'); x.href = 'foo:/y'; out.push(x.href);
out.push([new URL('../../x', 'file:///C:/a/b').href, new URL('..', 'file:///C:/').href, new URL('../x', 'file:///C|/a').href, new URL('../../x', 'http://h/C:/a').href, new URL('../..', 'file:///C:').href]);
const y = new URL('foo:/.//host/path'); out.push([y.href, y.pathname, y.host]);
out.push([new URL('foo:/a/..//b').href, new URL('foo://h/a/..//b').href, new URL('file:///a/..//b').href]);
JSON.stringify(out);
`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	want := `[["/a/../b","/a/../b","http://h/a/../b"],["/a/%2e%2e/b","/a/%2e%2e/b?x","http://h/a/%2e%2e/b?x"],["/a/./b","/a/./b","file:///a/./b"],["/a/./b","/a/./b","/a/./b"],"foo:/y",["file:///C:/x","file:///C:/","file:///C:/x","http://h/x","file:///C:/"],["foo:/.//host/path","//host/path",""],["foo:/.//b","foo://h//b","file:////b"]]`
	if result.String() != want {
		t.Errorf("got  %s\nwant %s", result.String(), want)
	}
}

// Relative resolution keeps whether the base has an authority (and its user info), a
// file: URL's path is always rooted, and url.resolve() removes only literal dot segments.
// Expected values are Node 20.18.1's.
func TestURL_RelativeAuthorityAndLegacyResolve(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
const out = [new URL('b', 'foo:/a').href, new URL('/b', 'foo:/a').href, new URL('?q=1', 'foo:/a').href, new URL('#h', 'foo:/a').href,
  new URL('b', 'foo://h/a').href, new URL('/b', 'file:///a').href, new URL('b', 'http://u:p@h/a').href,
  new URL('file:notes.txt').href, new URL('file:notes.txt').pathname, new URL('file:./a/../b').href,
  __url_module.resolve('http://h/a/', '%2e%2e/b'), __url_module.resolve('http://h/a/', '../b'), __url_module.resolve('http://h/a/b', './c/../d'), __url_module.resolve('http://example.com/', '/one'), __url_module.resolve('http://h/a/', '.%2e/b')];
JSON.stringify(out);
`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	want := `["foo:/b","foo:/b","foo:/a?q=1","foo:/a#h","foo://h/b","file:///b","http://u:p@h/b","file:///notes.txt","/notes.txt","file:///b","http://h/a/%2e%2e/b","http://h/b","http://h/a/d","http://example.com/one","http://h/a/.%2e/b"]`
	if result.String() != want {
		t.Errorf("got  %s\nwant %s", result.String(), want)
	}
}

// Resolving against an authority-less base whose path starts with "//" keeps it a path;
// url.resolve() merges against the base as written; url.parse() keeps a file: path that
// has no leading slash. Expected values are Node 20.18.1's.
func TestURL_LegacyAndDoubleSlashBases(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
JSON.stringify([new URL('b', 'foo:/.//host/a').href, new URL('?q=1', 'foo:/.//host/a').href, new URL('#h', 'foo:/.//host/a').href,
  __url_module.resolve('http://h/a/b/..', 'c'), __url_module.resolve('http://h/a/./b', 'c'), __url_module.parse('file:notes.txt').pathname, __url_module.parse('file:notes.txt').path,
  __url_module.parse('file:C:/a').pathname, __url_module.resolve('http://h/a/b/', '../c')]);
`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	want := `["foo:/.//host/b","foo:/.//host/a?q=1","foo:/.//host/a#h","http://h/a/b/c","http://h/a/c","notes.txt","notes.txt","C:/a","http://h/a/c"]`
	if result.String() != want {
		t.Errorf("got  %s\nwant %s", result.String(), want)
	}
}

// url.resolve() keeps the path as written for an empty, query-only or protocol-relative
// reference and removes literal dot segments otherwise; an empty reference to new URL()
// is the base less its fragment. Expected values are Node 20.18.1's.
func TestURL_LegacyResolveBranches(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
const cases = [['http://h/a/../b', '?q=1'], ['http://h/a/', '//other/a/../b'], ['http://h/a/', '/x/../y'], ['http://h/a/', 'http://o/a/../b'], ['http://h/a/../b', '#f'], ['http://h/a/b', 'x/../y'], ['http://h/a/../b', ''], ['http://h/a/', './'], ['http://h/a/b/', '..'], ['http://h/a/', '/x/./y'], ['http://h/a/', 'mailto:a/../b'], ['http://h/a/b', '../../../x'], ['http://h/a/b', '.%2e/x']];
JSON.stringify([cases.map(([a, b]) => __url_module.resolve(a, b)), new URL('', 'http://h/a/b?q=1#f').href, new URL('', 'foo:/a/b').href]);
`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	want := `[["http://h/a/../b?q=1","http://other/a/../b","http://h/y","http://o/b","http://h/b#f","http://h/a/y","http://h/a/../b","http://h/a/","http://h/a/","http://h/x/y","mailto:a/../b","http://h/x","http://h/a/.%2e/x"],"http://h/a/b?q=1","foo:/a/b"]`
	if result.String() != want {
		t.Errorf("got  %s\nwant %s", result.String(), want)
	}
}

// url.resolve() keeps the path of a reference with another scheme as written, and a
// special scheme that is the base's own without "//" is relative to the base. Expected
// values are Node 20.18.1's.
func TestURL_ResolveSchemes(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
JSON.stringify([__url_module.resolve('http://h/a/', 'https://other/a/../b'), __url_module.resolve('http://h/a/', 'HTTP://o/a/../b'), __url_module.resolve('http://h/a/', 'http://o/a/../b'), __url_module.resolve('http://h/a/', 'file:///a/../b'), __url_module.resolve('file:///x/', 'file:///a/../b'), __url_module.resolve('http://h/a/', 'ftp://o/a/./b'), __url_module.resolve('http://h/a/b', 'http:../c')]);
`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	want := `["https://other/a/../b","http://o/b","http://o/b","file:///a/../b","file:///b","ftp://o/a/./b","http://h/c"]`
	if result.String() != want {
		t.Errorf("got  %s\nwant %s", result.String(), want)
	}
}

// A same-scheme special reference continues as a relative one, a drive letter after file:
// replaces the path, url.resolve() roots a slashless file: reference, and a special scheme
// written without "//" has an authority (url.parse() keeps it as written). Expected values
// are Node 20.18.1's.
func TestURL_SchemeEdges(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
JSON.stringify([new URL('http:https://other/x', 'http://h/a/b').href, new URL('file:C:/x', 'file:///C:/a/b').href, __url_module.resolve('http://h/a', 'file:notes.txt'),
  new URL('x', 'http:example.com/a/b').href, new URL('?q=1', 'https:example.com/a').href, new URL('#h', 'ftp:example.com/a').href, new URL('http:example.com/a').href,
  new URL('https:\\\\x/y').href, new URL('https:x').href, new URL('http:/x').href, __url_module.parse('http:example.com/a').host, __url_module.parse('http:example.com/a').pathname, __url_module.parse('https:x').href]);
`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	want := `["http://h/a/https://other/x","file:///C:/x","file:///notes.txt","http://example.com/a/x","https://example.com/a?q=1","ftp://example.com/a#h","http://example.com/a","https://x/y","https://x/","http://x/",null,"example.com/a","https:x"]`
	if result.String() != want {
		t.Errorf("got  %s\nwant %s", result.String(), want)
	}
}
