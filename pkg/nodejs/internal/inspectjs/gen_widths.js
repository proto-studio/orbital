// Writes the table of column widths in inspect.js, between its "generated widths" markers, from
// the ICU that Node is built with: run `go generate` in this directory with Node 20.18.1 (ICU 75.1,
// Unicode 15.1, full ICU) as node.
//
// With ICU, Node's util.inspect measures a string with ICU's getStringWidth, which counts each
// code point as 2 columns when its East Asian Width is Fullwidth or Wide, or it's Ambiguous or
// Neutral with Emoji_Presentation; 0 when it's a control, format character (except U+00AD),
// enclosing or nonspacing mark, or emoji modifier; and 1 otherwise. This asks that same function
// about every code point, and lists where the width changes.
'use strict';
const fs = require('fs');
const path = require('path');
const { internalBinding } = require('internal/test/binding');

// The table must match the Node Orbital follows; another ICU counts other characters as wide.
// To move to a new Node, change these along with the version Orbital follows.
const want = { node: 'v20.18.1', icu: '75.1', unicode: '15.1' };
const have = { node: process.version, icu: process.versions.icu, unicode: process.versions.unicode };
if (have.node !== want.node || have.icu !== want.icu || have.unicode !== want.unicode) {
  throw new Error(`gen_widths.js needs Node ${want.node} (ICU ${want.icu}, Unicode ${want.unicode}); ` +
    `this is ${have.node} (ICU ${have.icu}, Unicode ${have.unicode})`);
}

const icu = internalBinding('icu');
const changes = [];
let last = -1;
let at = 0;
for (let point = 0; point <= 0x10ffff; point++) {
  const width = icu.getStringWidth(String.fromCodePoint(point));
  if (width === last) continue;
  changes.push((point - at).toString(36) + width);
  at = point;
  last = width;
}

const lines = [];
let line = '';
for (const change of changes) {
  if (line.length + change.length > 90) {
    lines.push(line);
    line = '';
  }
  line += change + ',';
}
lines.push(line.slice(0, -1));

const file = path.join(__dirname, 'inspect.js');
const source = fs.readFileSync(file, 'utf8');
const begin = '  // BEGIN generated widths';
const end = '  // END generated widths';
const table = [
  begin,
  `  // From ICU ${process.versions.icu} (Unicode ${process.versions.unicode}) in Node ${process.version}, by gen_widths.js.`,
  '  const WIDTHS =',
  lines.map((l) => `    '${l}'`).join(' +\n') + ';',
  end
].join('\n');
const from = source.indexOf(begin);
const to = source.indexOf(end);
if (from < 0 || to < 0) throw new Error('inspect.js has no generated widths markers');
fs.writeFileSync(file, source.slice(0, from) + table + source.slice(to + end.length));
