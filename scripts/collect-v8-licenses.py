#!/usr/bin/env python3
"""Copy the license notices for everything compiled into the shipped V8 libraries.

    scripts/collect-v8-licenses.py --src V8_SRC --out-dir OUT --dest DEST TARGET...

V8's BSD license, and the licenses of the code it bundles (ICU, libc++, abseil, zlib,
...), require binary redistributions to reproduce their notices. This asks ninja which
sources and headers went into each TARGET (paths relative to OUT, e.g.
obj/libv8_monolith.a), finds the bundled component each one belongs to, and copies that
component's license files into DEST at the same path they have in the V8 checkout:
DEST/LICENSE, DEST/third_party/icu/LICENSE, DEST/third_party/libc++/src/LICENSE.TXT, ...

Headers count, not just compiled sources, so header-only libraries (fp16, fast_float,
dragonbox, ...) are found too. The set follows whatever the GN args and V8 version
compile in, so a V8 update can't silently drop a notice: a component with no license
file (and no fallback below) fails the build.
"""

import argparse
import os
import shutil
import subprocess
import sys

# Bundled code whose license file isn't in the V8 checkout. The value is a file next to
# this script, shipped as <component>/LICENSE.
FALLBACKS = {
    # Chromium's PartitionAlloc. Its sources say "found in the LICENSE file", meaning
    # Chromium's top-level LICENSE, which its standalone repository doesn't carry.
    "third_party/partition_alloc": "licenses/chromium.LICENSE",
}

# Chromium's build wrappers for code that lives elsewhere in the checkout: the license
# for their files is the wrapped project's.
ALIASES = {
    "buildtools/third_party/libc++": "third_party/libc++/src",
    "buildtools/third_party/libc++abi": "third_party/libc++abi/src",
}

# Inputs that don't end up in the libraries. llvm-build is the compiler: its
# compiler-rt runtime is Apache-2.0 WITH LLVM-exception, which waives the notice
# requirement for binaries.
EXCLUDED = {"third_party/llvm-build"}

# Build scripts that ninja lists as inputs of generated sources.
TOOL_SUFFIXES = (".py", ".gn", ".gni")

README = """\
License notices for V8 and the third-party code compiled into the libraries in lib/.

Each file sits at the same path it has in the V8 source tree: LICENSE* at the top level
are V8's own, and third_party/<name>/ holds the license of each bundled component.
"""


def is_license(name):
    upper = name.upper()
    return upper.startswith("LICENSE") or upper.startswith("COPYING")


def license_files(directory):
    try:
        names = os.listdir(directory)
    except FileNotFoundError:
        return []
    return sorted(n for n in names if is_license(n) and os.path.isfile(os.path.join(directory, n)))


def component_root(rel):
    """third_party/icu/source/common/x.h -> third_party/icu (None outside third_party)."""
    parts = rel.split("/")
    if "third_party" not in parts[:-1]:
        return None
    i = parts.index("third_party")
    if i + 2 > len(parts) - 1:
        return None
    return "/".join(parts[: i + 2])


def ninja(out_dir, *args):
    exe = os.environ.get("NINJA", "ninja")
    return subprocess.run([exe, "-C", out_dir, *args], check=True, capture_output=True, text=True).stdout


def ninja_inputs(out_dir, targets):
    """Every file the targets are built from, sources plus the headers each object included."""
    inputs = [line.strip() for line in ninja(out_dir, "-t", "inputs", *targets).splitlines() if line.strip()]
    objects = [p for p in inputs if p.endswith((".o", ".obj"))]
    if not objects:
        sys.exit(f"ERROR: ninja lists no object files for {' '.join(targets)} in {out_dir}; build them first")
    headers = []
    missing = 0
    for i in range(0, len(objects), 500):
        for line in ninja(out_dir, "-t", "deps", *objects[i : i + 500]).splitlines():
            if line.startswith((" ", "\t")):
                headers.append(line.strip())
            elif line.endswith("deps not found"):
                missing += 1
    if missing:
        sys.exit(f"ERROR: ninja has no header deps for {missing} objects; header-only libraries can't be found")
    return inputs + headers


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--src", required=True, help="V8 checkout")
    ap.add_argument("--out-dir", required=True, help="GN build directory the targets were built in")
    ap.add_argument("--dest", required=True, help="directory to write the notices into (replaced)")
    ap.add_argument("targets", nargs="+", help="ninja targets that make up the shipped libraries")
    args = ap.parse_args()

    src = os.path.realpath(args.src)
    out_dir = os.path.realpath(args.out_dir)
    script_dir = os.path.dirname(os.path.realpath(__file__))

    # license file (relative to src, or "fallback:<path>") -> component it's for
    found = {}
    missing = set()

    for name in license_files(src):
        found[name] = "v8"
    if not found:
        sys.exit(f"ERROR: no LICENSE file at the top of {src}")

    for path in set(ninja_inputs(out_dir, args.targets)):
        if path.endswith(TOOL_SUFFIXES):
            continue
        full = os.path.realpath(os.path.join(out_dir, path))
        rel = os.path.relpath(full, src)
        if rel.startswith("..") or full.startswith(out_dir + os.sep):
            continue  # system headers, generated files
        rel = rel.replace(os.sep, "/")
        root = component_root(rel)
        if root is None or root in EXCLUDED:
            continue  # V8's own code, covered by its top-level LICENSE
        directory = os.path.dirname(rel)
        while True:
            names = license_files(os.path.join(src, directory))
            if names:
                for n in names:
                    found.setdefault(f"{directory}/{n}", root)
                break
            if directory == root:
                alias = ALIASES.get(root)
                if alias and license_files(os.path.join(src, alias)):
                    for n in license_files(os.path.join(src, alias)):
                        found.setdefault(f"{alias}/{n}", root)
                elif root in FALLBACKS:
                    found.setdefault(f"fallback:{root}", root)
                else:
                    missing.add(root)
                break
            directory = os.path.dirname(directory)

    if missing:
        sys.exit(
            "ERROR: no license file found for bundled code in: "
            + ", ".join(sorted(missing))
            + f"\n  Add its license to FALLBACKS in {os.path.relpath(__file__)}."
        )

    if os.path.exists(args.dest):
        shutil.rmtree(args.dest)
    os.makedirs(args.dest)
    with open(os.path.join(args.dest, "README.txt"), "w") as f:
        f.write(README)
    for key in sorted(found):
        if key.startswith("fallback:"):
            root = key[len("fallback:") :]
            source, target = os.path.join(script_dir, FALLBACKS[root]), f"{root}/LICENSE"
        else:
            source, target = os.path.join(src, key), key
        os.makedirs(os.path.join(args.dest, os.path.dirname(target)), exist_ok=True)
        shutil.copyfile(source, os.path.join(args.dest, target))

    components = sorted(set(found.values()))
    print(f">>> License notices for {len(components)} components → {args.dest}: {', '.join(components)}")


if __name__ == "__main__":
    main()
