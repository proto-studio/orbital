#!/usr/bin/env python3
"""Write a .tar.gz whose bytes depend only on the files' contents and permissions.

    scripts/reproducible-tar.py OUT.tar.gz DIR PATH...

Packs each PATH (relative to DIR) the way `tar -C DIR -czf OUT PATH...` would, but with
entries in sorted order, every mtime set to 0, owner and group set to 0 with no names,
and no timestamp or file name in the gzip header. Packaging the same libraries twice then
gives the same checksum, so a glue rebuild that changes nothing doesn't change the pin.
"""

import gzip
import os
import sys
import tarfile


def normalize(info: tarfile.TarInfo) -> tarfile.TarInfo:
    info.mtime = 0
    info.uid = info.gid = 0
    info.uname = info.gname = ""
    info.mode = 0o755 if info.isdir() or info.mode & 0o111 else 0o644
    return info


def entries(root: str, path: str):
    full = os.path.join(root, path)
    yield path
    if os.path.isdir(full) and not os.path.islink(full):
        for dirpath, dirnames, filenames in os.walk(full):
            dirnames.sort()
            rel = os.path.relpath(dirpath, root)
            if path == "." and rel != ".":
                rel = "./" + rel
            for name in sorted(dirnames + filenames):
                yield os.path.join(rel, name)


def main() -> None:
    if len(sys.argv) < 4:
        sys.exit(__doc__.strip())
    out, root, paths = sys.argv[1], sys.argv[2], sys.argv[3:]
    with open(out, "wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0, compresslevel=6) as gz:
            with tarfile.open(fileobj=gz, mode="w", format=tarfile.PAX_FORMAT) as tf:
                for path in paths:
                    for name in entries(root, path):
                        tf.add(os.path.join(root, name), arcname=name, recursive=False, filter=normalize)


if __name__ == "__main__":
    main()
