// Runs unchanged in Node and in Orbital. url_query.node.json is this script's output under
// Node v20.18.1; regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run TestURL_QueryMatchesNode
const finish = globalThis.done || ((value) => console.log(JSON.stringify(value, null, 2)));
const attempt = (fn) => {
  try {
    return fn();
  } catch (e) {
    return { name: e.name, code: e.code, message: e.message };
  }
};
const out = {};

// The query stays as parsed until searchParams changes.
const kept = new URL('http://h/?q&a=%41b+c');
out.kept = [kept.href, kept.search, [...kept.searchParams], String(kept.searchParams)];
kept.searchParams.append('x', 'y z');
out.afterAppend = [kept.href, kept.search];
const emptied = new URL('http://h/p?a=1#f');
emptied.searchParams.delete('a');
out.emptied = [emptied.href, emptied.search];
const bare = new URL('http://h/p?');
out.bare = [bare.href, bare.search];
out.encoded = [new URL('http://h/?é=ü "<>\'`{}|^').href, new URL('foo://h/?a b\'c').href];
out.malformed = attempt(() => { const u = new URL('http://h/?z=%zz'); return [u.href, [...u.searchParams]]; });

// search and href setters update the same searchParams object.
const u = new URL('http://h/?a=1');
const sp = u.searchParams;
u.href = 'http://x/?k=v';
out.hrefSetter = [sp === u.searchParams, sp.get('k'), String(sp)];
u.search = '?€=#';
out.searchSetter = [u.href, u.search, sp.get('€')];
u.search = '';
out.searchCleared = [u.href, sp.size];
u.searchParams.append('a', 'b');
out.appendAfterClear = u.href;
const sorted = new URL('http://h/?b=1&a=2&b=0');
sorted.searchParams.sort();
out.sorted = sorted.href;
const special = new URL('http://h/?a=1');
special.searchParams.set('a', "~!*()'");
out.serializedSet = special.href;

// URLSearchParams parses and serializes as application/x-www-form-urlencoded.
out.form = [[...new URLSearchParams('a=b+c&d=%20')], new URLSearchParams('a=b+c&d=%20').toString()];
out.loneSurrogate = new URLSearchParams([['a', '1'], ['b', '\ud800']]).toString();
out.record = new URLSearchParams({ x: 'é~' }).toString();
out.badTuple = attempt(() => new URLSearchParams([['a']]));
const pairs = new URLSearchParams('a=1&b=2&a=3');
pairs.delete('a', '3');
out.deleteValue = pairs.toString();
out.emptyParts = new URLSearchParams('?&&a&=b&%ZZ=%E2%82').toString();
out.badUTF8 = [...new URLSearchParams('%E2%82=1')];
finish(out);
