package runtime

// searchFlag opens a directory only to search it: O_EXEC, which with O_DIRECTORY is macOS's
// O_SEARCH. An older macOS that doesn't know it gets O_RDONLY instead.
const searchFlag = 0x40000000

// mkdirKeepsSticky says whether mkdir sets the sticky bit it is given. macOS's doesn't.
const mkdirKeepsSticky = false
