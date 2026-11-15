package nodejs

import (
	"io/fs"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// slowFS takes a while over each call, as a slow disk would.
type slowFS struct {
	*runtime.MemoryFilesystem
	delay time.Duration
}

func (s slowFS) Mkdir(path string, perm fs.FileMode) error {
	time.Sleep(s.delay)
	return s.MemoryFilesystem.Mkdir(path, perm)
}

func (s slowFS) ReadDir(path string) ([]runtime.DirEntry, error) {
	time.Sleep(s.delay)
	return s.MemoryFilesystem.ReadDir(path)
}

func (s slowFS) Rename(from, to string) error {
	time.Sleep(s.delay)
	return s.MemoryFilesystem.Rename(from, to)
}

func (s slowFS) OpenFile(path string, flag int, perm fs.FileMode) (runtime.File, error) {
	time.Sleep(s.delay)
	file, err := s.MemoryFilesystem.OpenFile(path, flag, perm)
	if err != nil {
		return nil, err
	}
	return slowFile{file, s.delay}, nil
}

type slowFile struct {
	runtime.File
	delay time.Duration
}

func (f slowFile) Write(p []byte) (int, error) {
	time.Sleep(f.delay)
	return f.File.Write(p)
}

// The callback and promise forms and write streams do their work off the JavaScript thread, so a timer
// due during a slow call runs before the call finishes, as in Node.
func TestFS_CallbacksRunOffTheThread(t *testing.T) {
	memory := runtime.NewMemoryFilesystem()
	got := runJS(t, &runtime.Config{Filesystem: slowFS{memory, 300 * time.Millisecond}}, `
const fs = require('fs');
const timerFirst = (start) => new Promise((resolve) => {
  const order = [];
  setTimeout(() => order.push('timer'), 20);
  start((err) => { order.push(err ? 'error ' + err.code : 'call'); resolve(order.join(' ')); });
});
(async () => {
  const results = {};
  results.mkdir = await timerFirst((cb) => fs.mkdir('/d', cb));
  results.readdir = await timerFirst((cb) => fs.readdir('/', cb));
  results.rename = await timerFirst((cb) => fs.rename('/d', '/e', cb));
  results.promise = await timerFirst((cb) => fs.promises.mkdir('/p').then(() => cb(), cb));
  results.open = await timerFirst((cb) => fs.open('/f', 'w', (err, fd) => { results.fd = fd; cb(err); }));
  results.write = await timerFirst((cb) => fs.write(results.fd, 'abc', cb));
  results.writeOrder = await new Promise((resolve) => {
    fs.write(results.fd, 'd', () => {});
    fs.write(results.fd, 'e', () => fs.close(results.fd, () => resolve(fs.readFileSync('/f', 'utf8'))));
  });
  results.stream = await timerFirst((cb) => {
    const ws = fs.createWriteStream('/g');
    ws.write('x');
    ws.end('y');
    ws.on('close', () => { results.streamContents = fs.readFileSync('/g', 'utf8'); cb(); });
  });
  delete results.fd;
  done(results);
})();
`)
	wantJSON(t, got, map[string]any{
		"mkdir": "timer call", "readdir": "timer call", "rename": "timer call", "promise": "timer call", "open": "timer call",
		"write": "timer call", "writeOrder": "abcde", "stream": "timer call", "streamContents": "xy",
	})
}

// A FileHandle's read, readv and writev are queued on the descriptor before they return, so a
// close made right after them waits for them, and every callback comes after the call returns.
func TestFS_DescriptorCallsKeepTheirOrder(t *testing.T) {
	memory := runtime.NewMemoryFilesystem()
	got := runJS(t, &runtime.Config{Filesystem: slowFS{memory, 20 * time.Millisecond}}, `
const fs = require('fs');
(async () => {
  const results = {};
  fs.writeFileSync('/f', 'abcdef');
  let handle = await fs.promises.open('/f', 'r');
  const reading = handle.read(Buffer.alloc(3), 0, 3, 0);
  const closing = handle.close();
  results.read = (await reading).buffer.toString();
  await closing;
  handle = await fs.promises.open('/g', 'w');
  const writing = handle.writev([Buffer.from('ab'), Buffer.from('cd')]);
  await handle.close();
  results.writev = [(await writing).bytesWritten, fs.readFileSync('/g', 'utf8')];
  handle = await fs.promises.open('/f', 'r');
  const vreading = handle.readv([Buffer.alloc(2), Buffer.alloc(2)], 1);
  await handle.close();
  results.readv = (await vreading).buffers.map(String);
  const fd = fs.openSync('/g', 'r+');
  let returned = false;
  results.emptyWritev = await new Promise((resolve) => { fs.writev(fd, [], (err, n) => resolve([returned, n])); returned = true; });
  returned = false;
  results.emptyReadv = await new Promise((resolve) => { fs.readv(fd, [Buffer.alloc(0)], (err, n) => resolve([returned, n])); returned = true; });
  fs.closeSync(fd);
  done(results);
})().catch((e) => done({ failed: String(e) }));
`)
	wantJSON(t, got, map[string]any{
		"read": "abc", "writev": []any{4.0, "abcd"}, "readv": []any{"bc", "de"},
		"emptyWritev": []any{true, 0.0}, "emptyReadv": []any{true, 0.0},
	})
}
