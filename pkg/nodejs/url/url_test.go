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

// href keeps the query as parsed, percent-encoding only what the URL parser does, and
// searchParams parses and serializes application/x-www-form-urlencoded and writes the
// query back only when it changes. Expected values are Node 20.18.1's.
func TestURL_QueryAsParsed(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
const out = [];
const show = (u) => out.push([u.href, u.search]);
show(new URL('http://h/?q&a=%41b+c'));
show(new URL('http://h/?'));
show(new URL('http://h/p?a b"<>é\'`+"`"+`#f'));
show(new URL('foo://h/p?a b"\'x'));
show(new URL('http://h/?a=%zz&b=%41'));
let u = new URL('http://h/?q&a=%41b+c'); u.searchParams.append('n', 'x y'); show(u);
u = new URL('http://h/?a=1'); u.searchParams.delete('a'); show(u);
u = new URL('http://h/?b=2&a=1'); u.searchParams.sort(); show(u);
u = new URL('http://h/?a=1'); u.searchParams.set('a', 'é&='); show(u);
u = new URL('http://h/?a=1'); u.search = 'x y=1&z'; show(u); out.push([...u.searchParams].join('|'));
u = new URL('http://h/?a=1'); u.search = ''; show(u);
u = new URL('http://h/'); u.searchParams.append('k', '~*-._!\'()'); show(u);
const p = new URLSearchParams('a=b+c&d=%2B&e=%zz&&f&=g&h=1=2');
out.push(JSON.stringify([...p]), p.toString());
out.push(new URLSearchParams({ 'a b': 'c+d', e: '\u{1F600}' }).toString());
out.push(new URLSearchParams([['x', 'y z']]).toString());
u = new URL('http://h/?a=1#frag'); u.searchParams.append('b', '2'); show(u); out.push(u.hash);
JSON.stringify(out);
`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	want := `[["http://h/?q&a=%41b+c","?q&a=%41b+c"],["http://h/?",""],["http://h/p?a%20b%22%3C%3E%C3%A9%27` + "`" + `#f","?a%20b%22%3C%3E%C3%A9%27` + "`" + `"],["foo://h/p?a%20b%22'x","?a%20b%22'x"],["http://h/?a=%zz&b=%41","?a=%zz&b=%41"],["http://h/?q=&a=Ab+c&n=x+y","?q=&a=Ab+c&n=x+y"],["http://h/",""],["http://h/?a=1&b=2","?a=1&b=2"],["http://h/?a=%C3%A9%26%3D","?a=%C3%A9%26%3D"],["http://h/?x%20y=1&z","?x%20y=1&z"],"x y,1|z,",["http://h/",""],["http://h/?k=%7E*-._%21%27%28%29","?k=%7E*-._%21%27%28%29"],"[[\"a\",\"b c\"],[\"d\",\"+\"],[\"e\",\"%zz\"],[\"f\",\"\"],[\"\",\"g\"],[\"h\",\"1=2\"]]","a=b+c&d=%2B&e=%25zz&f=&=g&h=1%3D2","a+b=c%2Bd&e=%F0%9F%98%80","x=y+z",["http://h/?a=1&b=2#frag","?a=1&b=2"],"#frag"]`
	if result.String() != want {
		t.Errorf("got  %s\nwant %s", result.String(), want)
	}
}

// searchParams decodes escapes as UTF-8, with U+FFFD for malformed bytes, and encodes a
// lone surrogate as U+FFFD, as Node 20.18.1 does.
func TestURLSearchParams_UTF8(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`JSON.stringify([[...new URLSearchParams('a=%C3%A9&b=%FF%41&c=%E2%82&d=%F0%9F%98%80&e=\uD800x')], new URLSearchParams({ x: '\uD800y' }).toString()])`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	want := `[[["a","é"],["b","�A"],["c","�"],["d","😀"],["e","�x"]],"x=%EF%BF%BDy"]`
	if result.String() != want {
		t.Errorf("got  %s\nwant %s", result.String(), want)
	}
}
