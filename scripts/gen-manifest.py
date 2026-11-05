#!/usr/bin/env python3
"""Assemble internal/v8dist/manifest.json from packaged artifacts + run metadata.

Reads every ``v8-<goos>-<goarch>.tar.gz.sha256`` in the dist directory, extracts
the checksum, and writes a manifest pinning the V8 version, module version, source
commit/run id, glue input and V8 build config hashes, the files glue builds use, and per-target {filename, sha256}. Consumers embed this manifest to
download the exact, checksum-verified Release asset for their platform.
"""
import argparse
import json
import os
import sys

# Canonical target ordering for a stable manifest. Intel macOS (darwin/amd64) is
# intentionally omitted: current V8 requires the macOS 15+ SDK (arm64 runners only).
ORDER = [
    ("linux", "amd64"),
    ("linux", "arm64"),
    ("darwin", "arm64"),
]


def parse_target(sha_filename):
    """v8-linux-amd64.tar.gz.sha256 -> ("linux", "amd64", "v8-linux-amd64.tar.gz")."""
    base = sha_filename[: -len(".sha256")]  # v8-<goos>-<goarch>.tar.gz
    stem = base[len("v8-"): -len(".tar.gz")]  # <goos>-<goarch>
    parts = stem.split("-")
    if len(parts) != 2:
        return None
    return parts[0], parts[1], base


def is_glue_build_file(filename):
    """The files glue rebuilds download. Consumers don't use these."""
    return filename == "v8-headers.tar.gz" or filename.startswith("v8-glue-sdk-")


def read_sha(path):
    with open(path) as f:
        return f.read().split()[0].strip()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dist-dir", required=True)
    ap.add_argument("--v8-version", required=True)
    ap.add_argument("--module-version", required=True)
    ap.add_argument("--commit", default="")
    ap.add_argument("--run-id", default="")
    ap.add_argument("--glue-inputs", default="")
    ap.add_argument("--v8-build-config", default="")
    ap.add_argument("--owner", required=True)
    ap.add_argument("--repo", required=True)
    ap.add_argument("--output", required=True)
    args = ap.parse_args()

    found = {}
    glue_build_files = {}
    for name in sorted(os.listdir(args.dist_dir)):
        if not name.endswith(".tar.gz.sha256"):
            continue
        filename = name[: -len(".sha256")]
        if is_glue_build_file(filename):
            glue_build_files[filename] = read_sha(os.path.join(args.dist_dir, name))
            continue
        parsed = parse_target(name)
        if not parsed:
            print(f"WARNING: skipping unrecognized checksum file {name}", file=sys.stderr)
            continue
        goos, goarch, filename = parsed
        found[(goos, goarch)] = {
            "goos": goos,
            "goarch": goarch,
            "filename": filename,
            "sha256": read_sha(os.path.join(args.dist_dir, name)),
        }

    if not found:
        sys.exit(f"ERROR: no v8-*.tar.gz.sha256 files found in {args.dist_dir}")

    ordered_keys = [k for k in ORDER if k in found]
    ordered_keys += [k for k in sorted(found) if k not in ORDER]
    targets = [found[k] for k in ordered_keys]

    manifest = {
        "v8_version": args.v8_version,
        "module_version": args.module_version,
        "source_commit": args.commit,
        "source_run_id": args.run_id,
        "glue_inputs": args.glue_inputs,
        "v8_build_config": args.v8_build_config,
        "glue_build_files": glue_build_files,
        "owner": args.owner,
        "repo": args.repo,
        "targets": targets,
    }

    with open(args.output, "w") as f:
        json.dump(manifest, f, indent=2)
        f.write("\n")

    print(f">>> wrote {args.output} with {len(targets)} target(s):")
    for t in targets:
        print(f"      {t['goos']}/{t['goarch']}  {t['filename']}  {t['sha256']}")


if __name__ == "__main__":
    main()
