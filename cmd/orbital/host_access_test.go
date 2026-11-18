package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The library defaults are closed, but the CLI gives scripts the host's files, network,
// processes, environment and details, as Node does.
func TestCLI_HasHostAccess(t *testing.T) {
	t.Setenv("ORBITAL_CLI_HOST_ACCESS", "visible")
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Write([]byte("tcp"))
			conn.Close()
		}
	}()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("http"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "t.js")
	source := fmt.Sprintf(`
const fs = require('fs');
const out = [];
out.push(fs.readFileSync(%[1]q, 'utf8'));
fs.writeFileSync(%[2]q, 'written');
out.push(require('child_process').execSync('echo spawned').toString().trim());
out.push(process.env.ORBITAL_CLI_HOST_ACCESS);
out.push(require('os').hostname() === %[3]q ? 'host' : require('os').hostname());
require('dns').lookup('localhost', (err) => {
  out.push(err ? err.code : 'resolved');
  const socket = require('net').connect(%[4]d, '127.0.0.1');
  socket.on('data', (d) => out.push(d.toString()));
  socket.on('close', async () => {
    out.push(await (await fetch(%[5]q)).text());
    require('http').get(%[5]q, (res) => {
      res.on('data', (d) => out.push(d.toString()));
      res.on('end', () => console.log(out.join(' ')));
    });
  });
});
`, filepath.Join(dir, "in.txt"), filepath.Join(dir, "out.txt"), host, ln.Addr().(*net.TCPAddr).Port, srv.URL)
	if err := os.WriteFile(script, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runOrbital(t, "", script)
	if want := "file spawned visible host resolved tcp http http\n"; code != 0 || out != want {
		t.Fatalf("exit %d, output %q, want %q", code, out, want)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "out.txt")); err != nil || string(data) != "written" {
		t.Errorf("out.txt = %q, %v", data, err)
	}
}
