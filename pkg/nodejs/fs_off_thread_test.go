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
