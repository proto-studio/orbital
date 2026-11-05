// Runs unchanged in Node and in Orbital. binary_fs.node.json is this script's output under
// Node v20.18.1 plus the bytes it left on disk, read back by the Go test rather than by the
// runtime under test. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestBinaryFS_MatchesNode'
const fs = require('fs');
const fsp = require('fs/promises');
const path = require('path');

const dir = globalThis.BINARY_DIR || process.argv[2];
const file = (name) => path.join(dir, name);
const seed = file('seed.bin'); // Written by the Go test: bytes 0 to 255.
const results = {};

const bin = Buffer.from([0x41, 0, 0x42, 0xff, 0, 0x43]);
const all = Buffer.from(Array.from({ length: 256 }, (_, i) => i));
const backing = new Uint8Array([9, 9, 0x41, 0, 0xff, 9]);

// [name, data, encoding or options]
const dataCases = [
  ['buffer', bin],
  ['leading_nul', Buffer.from([0, 0, 0, 1])],
  ['only_nul', Buffer.from([0])],
  ['all_bytes', all],
  ['uint8array', new Uint8Array([1, 0, 2, 255, 0])],
  ['subarray', backing.subarray(2, 5)],
  ['buffer_slice', all.subarray(250, 256)],
  ['uint16array', new Uint16Array([0x0041, 0xff00])],
  ['dataview', new DataView(backing.buffer, 2, 3)],
  ['empty_buffer', Buffer.alloc(0)],
  ['empty_string', ''],
  ['utf8', 'A\0Bÿ€😀'],
  ['lone_surrogate', 'a\ud800b'],
  ['named_utf8', 'é\0', 'utf8'],
  ['latin1', 'A\0B\xffC', 'latin1'],
  ['binary', 'A\0B\xffC', 'binary'],
  ['hex', '410042ff0043', 'hex'],
  ['base64', 'QQBC/wBD', 'base64'],
  ['utf16le', 'A\0B', 'utf16le'],
  ['ucs2', 'é', 'ucs2'],
  ['ascii', 'A\0é', 'ascii'],
  ['options_encoding', 'A\0B\xff', { encoding: 'latin1' }],
  ['options_null', 'é\0', { encoding: null }],
  ['options_no_encoding', 'é\0', {}],
];

const code = (fn) => {
  try {
    fn();
    return 'no error';
  } catch (e) {
    return e.code || e.name;
  }
};

// Reports strings by their UTF-16 code units, so JSON cannot normalize them.
const show = (value) => (typeof value === 'string'
  ? 'string:' + Buffer.from(value, 'utf16le').toString('hex')
  : (Buffer.isBuffer(value) ? 'buffer:' + value.toString('hex') : typeof value));

function testWrites() {
  for (const [name, data, options] of dataCases) {
    const args = options === undefined ? [data] : [data, options];
    fs.writeFileSync(file('writeFileSync_' + name), ...args);
    fs.writeFileSync(file('appendFileSync_' + name), 'prefix\0');
    fs.appendFileSync(file('appendFileSync_' + name), ...args);
  }
  fs.writeFileSync(file('overwrite'), Buffer.alloc(16, 0xff));
  fs.writeFileSync(file('overwrite'), bin);
  fs.appendFileSync(file('append_new'), bin);
  results.stat_size = fs.statSync(file('writeFileSync_all_bytes')).size;

  const large = Buffer.alloc(3 * 1024 * 1024);
  for (let i = 0; i < large.length; i++) large[i] = (i * 31) % 256;
  fs.writeFileSync(file('large'), large);
  results.large_read = fs.readFileSync(file('large')).equals(large);

  fs.copyFileSync(seed, file('copied'));
  fs.writeFileSync(file('to_rename'), bin);
  fs.renameSync(file('to_rename'), file('renamed'));
}

async function testAsyncWrites() {
  for (const [name, data, options] of dataCases) {
    const args = options === undefined ? [data] : [data, options];
    await new Promise((resolve, reject) => {
      fs.writeFile(file('writeFile_' + name), ...args, (err) => (err ? reject(err) : resolve()));
    });
    await fsp.writeFile(file('promises_writeFile_' + name), ...args);
    await fsp.appendFile(file('promises_appendFile_' + name), ...args);
  }
}

function testWriteSync() {
  const n = {};
  let fd = fs.openSync(file('writeSync_buffer'), 'w');
  n.buffer = fs.writeSync(fd, bin);
  n.buffer_offset = fs.writeSync(fd, bin, 1);
  n.buffer_offset_length = fs.writeSync(fd, bin, 1, 2);
  n.options = fs.writeSync(fd, bin, { offset: 3, length: 2 });
  n.uint8 = fs.writeSync(fd, new Uint8Array([0xfe, 0]));
  n.uint16 = fs.writeSync(fd, new Uint16Array([0x00ff]));
  n.dataview = fs.writeSync(fd, new DataView(backing.buffer, 2, 2));
  n.empty = fs.writeSync(fd, Buffer.alloc(0));
  fs.closeSync(fd);

  fd = fs.openSync(file('writeSync_string'), 'w');
  n.string = fs.writeSync(fd, 'a\0é');
  n.string_latin1 = fs.writeSync(fd, 'b\0\xff', null, 'latin1');
  n.string_hex = fs.writeSync(fd, '00ff00', null, 'hex');
  n.string_utf16 = fs.writeSync(fd, 'c', undefined, 'utf16le');
  fs.closeSync(fd);

  // A position writes there and leaves the current offset where it was.
  fd = fs.openSync(file('writeSync_position'), 'w');
  n.position_1 = fs.writeSync(fd, 'abcdef');
  n.position_2 = fs.writeSync(fd, 'X', 0);
  n.position_3 = fs.writeSync(fd, Buffer.from([0]), 0, 1, 1);
  n.position_4 = fs.writeSync(fd, Buffer.from([0xff]), { position: 2 });
  n.position_5 = fs.writeSync(fd, 'end');
  fs.closeSync(fd);

  fd = fs.openSync(file('writeSync_append'), 'a');
  fs.writeSync(fd, bin);
  fs.writeSync(fd, bin);
  fs.closeSync(fd);
  results.writeSync = n;
}

function testRead() {
  const read = {};
  read.none = show(fs.readFileSync(seed));
  read.undefined_options = show(fs.readFileSync(seed, undefined));
  read.null_options = show(fs.readFileSync(seed, null));
  read.empty_options = show(fs.readFileSync(seed, {}));
  read.null_encoding = show(fs.readFileSync(seed, { encoding: null }));
  read.buffer_encoding = code(() => fs.readFileSync(seed, 'buffer'));
  for (const encoding of ['utf8', 'utf-8', 'UTF8', 'latin1', 'binary', 'hex', 'base64', 'base64url', 'ascii', 'utf16le', 'ucs2']) {
    read[encoding] = show(fs.readFileSync(seed, encoding));
    read[encoding + '_options'] = show(fs.readFileSync(seed, { encoding }));
  }

  const r = {};
  r.is_buffer = Buffer.isBuffer(fs.readFileSync(seed));
  r.is_uint8array = fs.readFileSync(seed) instanceof Uint8Array;
  r.length = fs.readFileSync(seed).length;
  r.empty = show(fs.readFileSync(file('writeFileSync_empty_buffer')));
  r.empty_utf8 = show(fs.readFileSync(file('writeFileSync_empty_buffer'), 'utf8'));
  fs.writeFileSync(file('bom'), Buffer.from([0xef, 0xbb, 0xbf, 0x41]));
  r.bom = show(fs.readFileSync(file('bom'), 'utf8'));
  fs.writeFileSync(file('nul_text'), 'p\0q');
  r.nul_text = show(fs.readFileSync(file('nul_text'), 'utf8'));
  fs.writeFileSync(file('json'), '{"a":1}');
  r.json_parse_buffer = JSON.parse(fs.readFileSync(file('json'))).a;
  r.buffer_plus_string = fs.readFileSync(file('json')) + '';
  r.to_string = fs.readFileSync(file('json')).toString();
  // Without an encoding the bytes decode in Buffer, not as a read of UTF-8 text does.
  r.to_string_all_bytes = show(fs.readFileSync(seed).toString());
  r.utf8_read_all_bytes = show(fs.readFileSync(seed, 'utf8'));
  for (const [name, bytes] of [['lead_only', [0xff, 0x41]], ['truncated', [0xe2, 0x82]], ['truncated_then_ascii', [0xf0, 0x9f, 0x41]],
    ['overlong', [0xc0, 0x80]], ['overlong_3', [0xe0, 0x80, 0x80]], ['surrogate', [0xed, 0xa0, 0x80]], ['too_high', [0xf4, 0x90, 0x80, 0x80]],
    ['valid', [0xc3, 0xa9, 0xe2, 0x82, 0xac, 0xf0, 0x9f, 0x98, 0x80, 0, 0x41]], ['bom', [0xef, 0xbb, 0xbf, 0x41]]]) {
    fs.writeFileSync(file('decode_' + name), Buffer.from(bytes));
    r['decode_' + name] = show(fs.readFileSync(file('decode_' + name)).toString());
    r['decode_utf8_' + name] = show(fs.readFileSync(file('decode_' + name), 'utf8'));
  }
  results.readFileSync = read;
  results.read = r;
}

async function testAsyncRead() {
  const callback = {};
  const promised = {};
  const readFile = (...args) => new Promise((resolve, reject) => {
    fs.readFile(...args, (err, data) => (err ? reject(err) : resolve(data)));
  });
  for (const [name, options] of [['none', undefined], ['null', null], ['empty', {}], ['null_encoding', { encoding: null }],
    ['utf8', 'utf8'], ['UTF8', 'UTF8'], ['latin1', 'latin1'], ['hex', 'hex'], ['base64', 'base64'], ['utf16le', 'utf16le'],
    ['options', { encoding: 'latin1' }]]) {
    const args = options === undefined ? [seed] : [seed, options];
    callback[name] = show(await readFile(...args));
    promised[name] = show(await fsp.readFile(...args));
  }
  results.readFile = callback;
  results.promises_readFile = promised;
}

function testReadSync() {
  const r = {};
  const fd = fs.openSync(seed, 'r');
  const fill = () => Buffer.alloc(6, 0xee);

  let buf = fill();
  r.full_n = fs.readSync(fd, buf, 0, 6, 0);
  r.full = buf.toString('hex');
  buf = fill();
  r.offset_n = fs.readSync(fd, buf, 2, 3, 254);
  r.offset = buf.toString('hex');
  buf = fill();
  r.current_1_n = fs.readSync(fd, buf, 0, 2, null);
  r.current_1 = buf.toString('hex');
  buf = fill();
  r.current_2_n = fs.readSync(fd, buf, 0, 2, null);
  r.current_2 = buf.toString('hex');
  buf = fill();
  r.no_args_n = fs.readSync(fd, buf);
  r.no_args = buf.toString('hex');
  buf = fill();
  r.options_n = fs.readSync(fd, buf, { offset: 1, length: 2, position: 255 });
  r.options = buf.toString('hex');
  const array = new Uint8Array(4);
  r.uint8array_n = fs.readSync(fd, array, 0, 4, 0);
  r.uint8array = Array.from(array);
  const sub = new Uint8Array(6).subarray(2, 5);
  r.subarray_n = fs.readSync(fd, sub, 0, 3, 254);
  r.subarray = Array.from(new Uint8Array(sub.buffer));
  const view = new DataView(new ArrayBuffer(3));
  r.dataview_n = fs.readSync(fd, view, 0, 2, 255);
  r.dataview = Array.from(new Uint8Array(view.buffer));
  buf = fill();
  r.eof_n = fs.readSync(fd, buf, 0, 6, 256);
  r.eof = buf.toString('hex');
  r.zero_length = fs.readSync(fd, buf, 0, 0, 0);
  r.out_of_range = code(() => fs.readSync(fd, buf, 0, 7, 0));
  r.bad_offset = code(() => fs.readSync(fd, buf, 7, 1, 0));
  r.not_a_buffer = code(() => fs.readSync(fd, 'nope', 0, 1, 0));
  fs.closeSync(fd);
  results.readSync = r;
}

async function testStreams() {
  const collect = (stream) => new Promise((resolve, reject) => {
    const chunks = [];
    stream.on('data', (d) => chunks.push(d));
    stream.on('error', reject);
    stream.on('end', () => resolve(Buffer.concat(chunks).toString('hex')));
  });
  results.createReadStream = await collect(fs.createReadStream(seed));
  results.createReadStream_range = await collect(fs.createReadStream(seed, { start: 254, end: 255 }));
  results.createReadStream_large = (await collect(fs.createReadStream(file('large')))) === fs.readFileSync(file('large'), 'hex');
}

function testErrors() {
  const e = {};
  const target = file('errors');
  const fd = fs.openSync(file('errors_fd'), 'w');
  for (const [name, value] of [['number', 42], ['null', null], ['undefined', undefined], ['object', {}], ['array', [1, 2]], ['boolean', true]]) {
    e['writeFileSync_' + name] = code(() => fs.writeFileSync(target, value));
    e['appendFileSync_' + name] = code(() => fs.appendFileSync(target, value));
    e['writeFile_' + name] = code(() => fs.writeFile(target, value, () => {}));
    e['writeSync_' + name] = code(() => fs.writeSync(fd, value));
  }
  e.writeFile_no_callback = code(() => fs.writeFile(target, 'x'));
  e.readFile_no_callback = code(() => fs.readFile(seed));
  e.writeSync_out_of_range = code(() => fs.writeSync(fd, bin, 7));
  e.writeSync_too_long = code(() => fs.writeSync(fd, bin, 0, 7));
  e.writeFileSync_bad_encoding = code(() => fs.writeFileSync(target, 'x', 'nope'));
  e.readFileSync_bad_encoding = code(() => fs.readFileSync(seed, 'nope'));
  e.readFileSync_missing = code(() => fs.readFileSync(file('missing')));
  e.readFileSync_directory = code(() => fs.readFileSync(dir));
  fs.closeSync(fd);
  results.errors = e;
}

async function testFileHandle() {
  const h = {};
  let handle = await fsp.open(file('handle_write'), 'w');
  h.typeof_fd = typeof handle.fd;
  h.write_buffer = (await handle.write(bin)).bytesWritten;
  h.write_range = (await handle.write(bin, 1, 2)).bytesWritten;
  h.write_string = (await handle.write('é\0')).bytesWritten;
  h.write_string_latin1 = (await handle.write('\xff\0', null, 'latin1')).bytesWritten;
  h.write_position = (await handle.write(Buffer.from([0x7a]), 0, 1, 0)).bytesWritten;
  await handle.writeFile(Buffer.from([0, 0xfe]));
  h.stat_size = (await handle.stat()).size;
  await handle.close();

  handle = await fsp.open(seed);
  const buf = Buffer.alloc(6, 0xee);
  h.read_n = (await handle.read(buf, 1, 3, 254)).bytesRead;
  h.read_same_buffer = (await handle.read(buf, 1, 3, 254)).buffer === buf;
  h.read = buf.toString('hex');
  h.read_options = (await handle.read({ buffer: Buffer.alloc(2), position: 255 })).buffer.toString('hex');
  h.read_file = (await handle.readFile()).toString('hex');
  h.read_file_again = (await handle.readFile()).length;
  await handle.close();

  handle = await fsp.open(file('handle_rw'), 'w+');
  await handle.write(bin);
  const back = Buffer.alloc(6);
  h.read_back_n = (await handle.read(back, 0, 6, 0)).bytesRead;
  h.read_back = back.toString('hex');
  await handle.close();

  h.missing = await fsp.open(file('missing')).then(() => 'no error', (e) => e.code);

  fs.writeFileSync(file('truncate_short'), all);
  await fsp.truncate(file('truncate_short'), 130);
  fs.writeFileSync(file('truncate_long'), bin);
  await fsp.truncate(file('truncate_long'), 8);
  fs.writeFileSync(file('truncate_zero'), bin);
  await fsp.truncate(file('truncate_zero'));
  results.file_handle = h;
}

async function main() {
  testWrites();
  await testAsyncWrites();
  testWriteSync();
  testRead();
  await testAsyncRead();
  testReadSync();
  await testStreams();
  testErrors();
  await testFileHandle();
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
