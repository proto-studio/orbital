#!/usr/bin/env python3
"""Split a (GNU) static archive into N parts, each a valid archive.

V8's libv8_monolith.a is larger than GitHub's 100MB per-file limit (V8 15.x is
~126MB). Git LFS cannot be used because `go get` / the Go module proxy do not run
the LFS smudge filter, so the archives must be committed as plain files under
100MB. We therefore split the monolith into several smaller archives and link
them all together (inside --start-group so the linker resolves cross-references
between the parts).

This operates at the ar byte level rather than via `ar x`, because:
  * V8's monolith has many members that share a basename (allocation.o, cpu.o,
    ...), which `ar x` would clobber on extraction.
  * We must preserve the GNU long-name table (`//`) so `/offset` member names
    stay valid; copying it verbatim into each part keeps the offsets correct.

The per-part symbol index (`/`) is regenerated afterwards with `ranlib`.
"""
import argparse
import os
import subprocess
import sys

ARMAG = b"!<arch>\n"
HDR_SIZE = 60


def parse_members(data):
    """Yield (name_field, raw_bytes) for each member, where raw_bytes is the
    60-byte header plus data plus any 1-byte padding (i.e. a self-contained
    chunk that can be concatenated directly)."""
    if data[:8] != ARMAG:
        sys.exit("ERROR: not an ar archive (bad magic)")
    pos = 8
    n = len(data)
    while pos < n:
        if pos + HDR_SIZE > n:
            sys.exit("ERROR: truncated archive header")
        header = data[pos:pos + HDR_SIZE]
        if header[58:60] != b"\x60\n":
            sys.exit(f"ERROR: bad member magic at offset {pos}")
        name_field = header[0:16].decode("ascii", "replace")
        size = int(header[48:58].decode("ascii").strip())
        data_start = pos + HDR_SIZE
        data_end = data_start + size
        pad = size & 1
        raw = data[pos:data_end + pad]
        yield name_field, raw
        pos = data_end + pad


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("archive")
    ap.add_argument("--parts", type=int, required=True)
    ap.add_argument("--out-prefix", required=True,
                    help="output path prefix; parts are <prefix>_0.a, <prefix>_1.a, ...")
    ap.add_argument("--ranlib", default="ranlib")
    args = ap.parse_args()

    with open(args.archive, "rb") as f:
        data = f.read()

    long_name_table = None   # the `//` member, replicated into every part
    regular = []             # list of raw member chunks
    for name_field, raw in parse_members(data):
        name = name_field.rstrip()
        if name == "/" or name == "/SYM64/":
            continue  # symbol table: regenerated per-part by ranlib
        if name == "//":
            long_name_table = raw
            continue
        regular.append(raw)

    if not regular:
        sys.exit("ERROR: no regular members found")

    # Round-robin members across parts for even sizes.
    buckets = [[] for _ in range(args.parts)]
    sizes = [0] * args.parts
    for raw in sorted(regular, key=len, reverse=True):
        i = sizes.index(min(sizes))
        buckets[i].append(raw)
        sizes[i] += len(raw)

    outputs = []
    for i, bucket in enumerate(buckets):
        out = f"{args.out_prefix}_{i}.a"
        with open(out, "wb") as f:
            f.write(ARMAG)
            if long_name_table is not None:
                f.write(long_name_table)
            for raw in bucket:
                f.write(raw)
        subprocess.run([args.ranlib, out], check=True)
        outputs.append(out)
        print(f">>> wrote {out} ({len(bucket)} members, {os.path.getsize(out)} bytes)")

    print(f">>> split {args.archive} into {args.parts} parts")


if __name__ == "__main__":
    main()
