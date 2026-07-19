#!/usr/bin/env python3
"""Pre-compile the cgo C++ glue (v8go.cc) into a static library.

The glue calls into V8 whose objects are compiled with Chromium's custom libc++
(the std::__Cr:: inline namespace) plus pointer-compression / sandbox layouts.
If the glue were compiled by cgo with the system libstdc++, the std:: symbols
would not match and linking fails (undefined reference to
v8::platform::NewDefaultPlatform(..., std::__Cr::unique_ptr<...>)).

To guarantee an identical ABI we reuse V8's OWN compile command (from the GN
`compile_commands.json`), swapping only the input/output for our glue source.
The result is archived into a per-platform libv8go_glue.a that consumers link
with plain gcc alongside libv8_monolith.a.
"""
import argparse
import json
import os
import shlex
import subprocess
import sys


def is_cpp_source(path: str) -> bool:
    return path.endswith((".cc", ".cpp", ".cxx", ".c++"))


def get_output(tokens):
    """Return the -o value from a tokenized compile command, or None."""
    for i, tok in enumerate(tokens):
        if tok == "-o" and i + 1 < len(tokens):
            return tokens[i + 1]
        if tok.startswith("-o") and len(tok) > 2:
            return tok[2:]
    return None


def pick_entry(entries):
    """Pick a representative default-toolchain C++ compile command.

    Prefer a libplatform source (it includes both <v8.h> and
    <libplatform/libplatform.h>, exactly like the glue). Fall back to any
    default-toolchain C++ unit built with the sandbox define.
    """
    candidates = []
    for e in entries:
        cmd = e.get("command")
        tokens = shlex.split(cmd) if cmd else e.get("arguments", [])
        if not tokens:
            continue
        out = get_output(tokens)
        # Default (target) toolchain outputs live directly under obj/. Host
        # snapshot toolchains (e.g. clang_x64_v8_arm64/obj/...) are the wrong
        # architecture, so skip them.
        if not out or not out.startswith("obj/"):
            continue
        if not is_cpp_source(e.get("file", "")):
            continue
        candidates.append((e, tokens))

    def score(item):
        e, tokens = item
        f = e.get("file", "")
        s = 0
        if "libplatform" in f:
            s += 10
        if any("V8_ENABLE_SANDBOX" in t for t in tokens):
            s += 1
        return s

    candidates.sort(key=score, reverse=True)
    return candidates[0] if candidates else None


DROP_FLAGS = {"-c", "-MD", "-MMD", "-MP"}
DROP_FLAGS_WITH_ARG = {"-o", "-MF", "-MT", "-MQ", "-MJ"}

# Clang flags that start with "-I" / "-i" but are NOT -I include dirs.
_NOT_DASH_I = (
    "-include",
    "-imacros",
    "-idirafter",
    "-iframework",
    "-iprefix",
    "-iwithprefix",
    "-iwithprefixbefore",
)


def build_compile_args(tokens, source_file):
    """Strip file-specific flags, keeping everything ABI-relevant."""
    compiler = tokens[0]
    flags = []
    skip = False
    src_base = os.path.basename(source_file)
    for i, tok in enumerate(tokens[1:], start=1):
        if skip:
            skip = False
            continue
        if tok in DROP_FLAGS:
            continue
        if tok in DROP_FLAGS_WITH_ARG:
            skip = True
            continue
        if any(tok.startswith(p) for p in ("-MF", "-MT", "-MQ", "-MJ")) and len(tok) > 3:
            continue
        if tok.startswith("-o") and len(tok) > 2:
            continue
        # Drop the input source token itself.
        if tok == source_file or os.path.basename(tok) == src_base:
            continue
        flags.append(tok)
    return compiler, flags


def _is_fused_include(tok: str, prefix: str) -> bool:
    """True for fused forms like -I/path or -isystem/path (not -include …)."""
    if not tok.startswith(prefix) or len(tok) <= len(prefix):
        return False
    if prefix == "-I" and any(tok.startswith(p) for p in _NOT_DASH_I):
        return False
    return True


def drop_missing_includes(flags, cwd):
    """Remove -I/-isystem entries whose directories do not exist on disk.

    compile_commands.json embeds absolute/relative -I paths into the full V8
    source tree. Glue-only rebuilds often supply headers via --include (from a
    headers artifact or deps/v8/include) and may not have v8-build/v8 checked
    out — dropping missing -I keeps those stale paths from breaking the compile.
    """
    out = []
    skip = False
    for i, tok in enumerate(flags):
        if skip:
            skip = False
            continue
        dropped = False
        for p in ("-I", "-isystem"):
            if tok == p and i + 1 < len(flags):
                inc = flags[i + 1]
                path = inc if os.path.isabs(inc) else os.path.join(cwd, inc)
                if not os.path.isdir(path):
                    print(f">>> Dropping missing include: {p} {inc}")
                    skip = True
                    dropped = True
                break
            if _is_fused_include(tok, p):
                inc = tok[len(p) :]
                path = inc if os.path.isabs(inc) else os.path.join(cwd, inc)
                if not os.path.isdir(path):
                    print(f">>> Dropping missing include: {tok}")
                    dropped = True
                break
        if not dropped:
            out.append(tok)
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out-dir", required=True, help="V8 GN output dir (has compile_commands.json)")
    ap.add_argument("--src", required=True, help="glue C++ source (v8go.cc)")
    ap.add_argument("--output", required=True, help="output static lib (libv8go_glue.a)")
    ap.add_argument("--include", action="append", default=[], help="extra -I dirs (absolute)")
    ap.add_argument(
        "--drop-missing-includes",
        action="store_true",
        help="skip -I/-isystem dirs that do not exist (for headers-artifact glue rebuilds)",
    )
    ap.add_argument("--ar", default="ar", help="archiver")
    args = ap.parse_args()

    out_dir = os.path.abspath(args.out_dir)
    src = os.path.abspath(args.src)
    output = os.path.abspath(args.output)
    cc_json = os.path.join(out_dir, "compile_commands.json")

    if not os.path.exists(cc_json):
        sys.exit(f"ERROR: {cc_json} not found (did gn gen run with --export-compile-commands?)")

    with open(cc_json) as f:
        entries = json.load(f)

    picked = pick_entry(entries)
    if not picked:
        sys.exit("ERROR: no suitable V8 C++ compile command found in compile_commands.json")
    entry, tokens = picked
    print(f">>> Using V8 compile flags from: {entry.get('file')}")

    compiler, flags = build_compile_args(tokens, entry.get("file", ""))
    if args.drop_missing_includes:
        flags = drop_missing_includes(flags, out_dir)

    obj = os.path.join(out_dir, "v8go_glue.o")
    cmd = [compiler] + flags
    for inc in args.include:
        cmd += ["-I", os.path.abspath(inc)]
    # -fPIC keeps the glue linkable into both PIE and no-pie Go binaries.
    cmd += ["-fPIC", "-c", src, "-o", obj]

    print(">>> Compiling glue:\n    " + " ".join(shlex.quote(c) for c in cmd))
    subprocess.run(cmd, cwd=out_dir, check=True)

    if os.path.exists(output):
        os.remove(output)
    ar_cmd = [args.ar, "rcs", output, obj]
    print(">>> Archiving: " + " ".join(shlex.quote(c) for c in ar_cmd))
    subprocess.run(ar_cmd, check=True)
    print(f">>> Built glue: {output}")


if __name__ == "__main__":
    main()
