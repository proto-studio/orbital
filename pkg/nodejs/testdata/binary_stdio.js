// Runs unchanged in Node and in Orbital. binary_stdio.node.json holds the bytes this script
// put on stdout and stderr under Node v20.18.1, read from the pipes by the Go test.
// Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestBinaryStdio_MatchesNode'
const { Console } = require('console');

// 0x41 'A', NUL, 0x42 'B', 0xFF (never valid in UTF-8), NUL, 0x43 'C'.
const bin = Buffer.from([0x41, 0, 0x42, 0xff, 0, 0x43]);
const all = Buffer.from(Array.from({ length: 256 }, (_, i) => i));

function testWrite(stream) {
  stream.write(bin);
  stream.write(all);
  stream.write(all.subarray(250, 256));
  stream.write(new Uint8Array([1, 0, 2, 255, 0]));
  // NUL and 0xFF (never valid in UTF-8) between 9s; the subarray writes only the middle two.
  stream.write(new Uint8Array([9, 0, 0xff, 9]).subarray(1, 3));
  stream.write(Buffer.alloc(0));
  stream.write('');
  stream.write('A\0Bÿ€😀');
  // \ud800 is a lone UTF-16 high surrogate, which UTF-8 can't encode.
  stream.write('a\ud800b');
  stream.write('\0');
  // \xff is U+00FF 'ÿ', the single byte 0xFF in latin1.
  stream.write('A\0B\xffC', 'latin1');
  // \xff is U+00FF 'ÿ', the single byte 0xFF in latin1.
  stream.write('A\0B\xffC', 'binary');
  stream.write('410042ff0043', 'hex');
  stream.write('QQBC/wBD', 'base64');
  stream.write('A\0B', 'utf16le');
  stream.write('é\0', 'utf8');
  stream.write('A\0é', 'ascii');
}

function code(fn) {
  try {
    fn();
    return 'no error';
  } catch (e) {
    return e.code || e.name;
  }
}

function testWriteArguments(stream) {
  const returned = stream.write('r');
  stream.write(String(returned));
  for (const value of [42, {}, [1], true, undefined]) {
    stream.write(String(code(() => stream.write(value))));
  }
  stream.write(String(code(() => stream.write(null))));
  stream.write(String(code(() => stream.write('x', 'nope'))));
}

function testCallbacks(stream, label) {
  return new Promise((resolve) => {
    stream.write(bin, () => {
      stream.write(label + ' buffer callback\0');
      stream.write('ff00', 'hex', () => {
        stream.write(label + ' encoding callback\0');
        resolve();
      });
    });
    stream.write(label + ' after write\0');
  });
}

function testConsole() {
  console.log('p\0q');
  console.log('\0');
  console.log('a\0b', 'c\0d');
  console.log('%s|%s', 'a\0b', 'c');
  console.log('x\0%s', 'y');
  console.log('é\0€😀');
  // \ud800 is a lone UTF-16 high surrogate, which UTF-8 can't encode.
  console.log('a\ud800b');
  console.log(['a\0b', 'c']);
  console.log({ key: 'a\0b' });
  console.log([['a\0b']]);
  console.info('info\0');
  console.debug('debug\0');
  console.warn('warn\0');
  console.error('error\0');
  console.group('g\0h');
  console.log('i\0j');
  console.groupEnd();
  console.dir('d\0e');
  console.dir(['d\0e']);
  console.count('c\0d');
  console.assert(false, 'a\0b');
  new Console(process.stdout, process.stderr).log('class\0log');
  new Console(process.stdout, process.stderr).error('class\0error');
}

async function main() {
  testWrite(process.stdout);
  testWrite(process.stderr);
  testWriteArguments(process.stdout);
  await testCallbacks(process.stdout, 'out');
  await testCallbacks(process.stderr, 'err');
  testConsole();
  // NUL, then 0xFE (never valid in UTF-8).
  process.stdout.write(Buffer.from([0, 0xfe]));
  // 0xFF (never valid in UTF-8), then NUL.
  process.stdout.end(Buffer.from([0xff, 0]));
}

main();
