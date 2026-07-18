'use strict';
// Exercise React 19 server-side rendering on Orbital with the REAL published
// `react` + `react-dom` packages (the exact prebuilt CJS bundles npm users ship
// to production). The point of React-DOM/SSR as a core package is to validate
// that Orbital can execute React's renderer end-to-end: the module system (React
// switches production/development bundles on process.env.NODE_ENV and pulls in
// ./cjs/* via CommonJS require), the JS engine surface React's runtime leans on
// (Map/Set/WeakMap, TextEncoder, queueMicrotask/setImmediate, etc.), and — for
// streaming SSR — Orbital's Node stream + HTTP server stack.
//
// Components are built with React.createElement (no JSX build step needed, so the
// test runs straight on Orbital). Run from the isolated install dir created by the
// manifest (cwd == .cache/react/.ssr), where node_modules/{react,react-dom} live.
const path = require('path');
const assert = require('assert');
const http = require('http');

const nm = path.join(process.cwd(), 'node_modules');
const React = require(path.join(nm, 'react', 'index.js'));
// react-dom/server for Node: renderToString + renderToStaticMarkup (legacy sync)
// and renderToPipeableStream (streaming).
const ReactDOMServer = require(path.join(nm, 'react-dom', 'server.node.js'));

const h = React.createElement;
const { useId, useMemo, useContext, createContext } = React;
const { renderToString, renderToStaticMarkup, renderToPipeableStream } =
  ReactDOMServer;

assert.strictEqual(typeof renderToString, 'function', 'renderToString export');
assert.strictEqual(
  typeof renderToStaticMarkup,
  'function',
  'renderToStaticMarkup export'
);
assert.strictEqual(
  typeof renderToPipeableStream,
  'function',
  'renderToPipeableStream export'
);
assert.ok(/^19\./.test(React.version), 'expected React 19, got ' + React.version);
console.log('react-dom/server surface OK (React ' + React.version + ')');

// --- component tree (function components, props, context, hooks, keyed lists) ---
const ThemeContext = createContext('light');

function Badge(props) {
  // useId exercises React's SSR id generator (needs a stable per-render counter).
  const id = useId();
  return h('span', { className: 'badge', 'data-testid': id }, props.label);
}

function Item(props) {
  const theme = useContext(ThemeContext);
  return h('li', { className: 'item item--' + theme }, props.text);
}

function List(props) {
  return h(
    'ul',
    { className: 'list' },
    props.items.map((t, i) => h(Item, { key: i, text: t }))
  );
}

function App(props) {
  const heading = useMemo(() => props.title.toUpperCase(), [props.title]);
  return h(
    ThemeContext.Provider,
    { value: 'dark' },
    h(
      'div',
      { className: 'app' },
      h('h1', null, heading),
      h(Badge, { label: 'v1' }),
      h(List, { items: props.items })
    )
  );
}

function makeTree(title, items) {
  return h(App, { title: title, items: items });
}

// --- 1. renderToString ---
(function testRenderToString() {
  const html = renderToString(makeTree('hello', ['alpha', 'beta', 'gamma']));
  assert.ok(html.includes('<h1>HELLO</h1>'), 'useMemo-derived heading rendered');
  assert.ok(html.includes('<ul class="list">'), 'list container rendered');
  const items = html.match(/<li class="item item--dark">/g) || [];
  assert.strictEqual(items.length, 3, 'context propagated to 3 keyed <li> items');
  assert.ok(html.includes('>alpha<'), 'item text rendered');
  assert.ok(
    /<span class="badge" data-testid="[^"]+">v1<\/span>/.test(html),
    'useId produced a stable id attribute'
  );
  console.log('renderToString OK');
})();

// --- 2. renderToStaticMarkup + HTML escaping ---
(function testStaticMarkup() {
  const markup = renderToStaticMarkup(
    h('div', null, h('span', null, '<script>alert(1)</script> & "quotes"'))
  );
  assert.ok(
    markup.includes('&lt;script&gt;alert(1)&lt;/script&gt;'),
    'dangerous markup is HTML-escaped'
  );
  assert.ok(markup.includes('&amp;'), 'ampersand escaped');
  // renderToStaticMarkup omits the streaming/hydration template comments that
  // renderToString/pipeable output can include.
  assert.ok(!markup.includes('<!--'), 'static markup has no comment markers');
  console.log('renderToStaticMarkup OK');
})();

// --- 3. renderToPipeableStream piped into a real HTTP response ---
function testPipeableStream() {
  return new Promise((resolve, reject) => {
    const server = http.createServer((req, res) => {
      res.setHeader('Content-Type', 'text/html; charset=utf-8');
      const { pipe } = renderToPipeableStream(
        makeTree('streamed', ['one', 'two']),
        {
          onShellReady() {
            res.statusCode = 200;
            pipe(res);
          },
          onError(err) {
            reject(err);
          },
        }
      );
    });

    server.listen(0, () => {
      const port = server.address().port;
      http
        .get({ host: '127.0.0.1', port: port, path: '/' }, (res) => {
          let body = '';
          res.setEncoding('utf8');
          res.on('data', (c) => (body += c));
          res.on('end', () => {
            server.close();
            try {
              assert.strictEqual(res.statusCode, 200, 'SSR stream returns 200');
              assert.ok(
                body.includes('<h1>STREAMED</h1>'),
                'streamed heading present'
              );
              assert.ok(
                body.includes('>one<') && body.includes('>two<'),
                'streamed list items present'
              );
              console.log('renderToPipeableStream -> HTTP response OK');
              resolve();
            } catch (e) {
              reject(e);
            }
          });
        })
        .on('error', reject);
    });
  });
}

testPipeableStream()
  .then(() => {
    console.log('ALL React SSR smoke checks passed');
    process.exit(0);
  })
  .catch((err) => {
    console.error('React SSR smoke FAILED:', err && err.stack ? err.stack : err);
    process.exit(1);
  });
