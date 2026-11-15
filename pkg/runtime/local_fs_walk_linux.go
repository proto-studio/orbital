package runtime

import "golang.org/x/sys/unix"

// searchFlag opens a directory only as a place to walk from, with O_PATH, which needs no
// read permission.
const searchFlag = unix.O_PATH
