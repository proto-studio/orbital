#!/usr/bin/env python3
"""Run the core-package regression suites described by a manifest.

Reads tests/core-packages/manifest.json, and for each package:

  1. Clones the upstream project from GitHub into a cache dir (at a pinned ref).
  2. Runs the package's ``install`` commands with the host toolchain
     (real node/npm) to fetch dependencies and build.
  3. Runs the package's ``test`` commands with the Orbital binary exposed as the
     ``$ORBITAL`` environment variable, so a project's own test suite is executed
     on the Orbital runtime.

A non-zero exit status is returned if any package's install or test step fails,
so this can be wired into CI as a pass/fail gate.

Manifest schema (tests/core-packages/manifest.json):

    {
      "packages": [
        {
          "name":        "mjml",              # required, unique
          "description": "…",                  # optional
          "repo":        "https://…/x.git",    # required, git URL
          "ref":         "v4.15.3",            # optional (default branch if omitted)
          "subdir":      "packages/foo",       # optional cwd for all commands
          "env":         { "CI": "true" },     # optional extra env vars
          "install":     ["npm install", …],   # optional list of shell commands
          "test":        ["\"$ORBITAL\" t.js"] # required list of shell commands
        }
      ]
    }

Commands run through ``/bin/bash`` in the package directory. ``$ORBITAL`` is set
to the absolute path of the Orbital binary for every command.
"""
import argparse
import json
import os
import shutil
import subprocess
import sys


def log(msg=""):
    print(msg, flush=True)


def run(cmd, cwd=None, env=None):
    """Run a shell command, streaming output. Returns the exit code."""
    log(f"    $ {cmd}")
    return subprocess.run(
        cmd, cwd=cwd, env=env, shell=True, executable="/bin/bash"
    ).returncode


def ensure_repo(pkg, cache_dir, offline):
    """Clone (or update) the package repo. Returns the checkout path."""
    name = pkg["name"]
    repo = pkg["repo"]
    ref = pkg.get("ref")
    target = os.path.join(cache_dir, name)
    git_dir = os.path.join(target, ".git")

    if offline:
        if not os.path.isdir(git_dir):
            raise RuntimeError(
                f"--offline set but {target} is not a git checkout; clone it first"
            )
        log(f">>> [{name}] offline: using existing checkout at {target}")
        return target

    if os.path.isdir(git_dir):
        log(f">>> [{name}] updating existing checkout at {target}")
        run(f'git -C "{target}" fetch --tags --force origin')
        if ref:
            if run(f'git -C "{target}" checkout --force "{ref}"') != 0:
                # ref may be a remote branch not yet checked out locally
                run(f'git -C "{target}" checkout --force -B "{ref}" "origin/{ref}"')
            run(f'git -C "{target}" reset --hard "{ref}"')
        return target

    os.makedirs(cache_dir, exist_ok=True)
    if os.path.isdir(target):
        shutil.rmtree(target)

    log(f">>> [{name}] cloning {repo}" + (f" @ {ref}" if ref else ""))
    if ref:
        # Fast path: shallow clone of a tag or branch.
        rc = run(f'git clone --depth 1 --branch "{ref}" "{repo}" "{target}"')
        if rc != 0:
            # Fallback: full clone then checkout (handles arbitrary commit SHAs).
            if os.path.isdir(target):
                shutil.rmtree(target)
            if run(f'git clone "{repo}" "{target}"') != 0:
                raise RuntimeError(f"failed to clone {repo}")
            if run(f'git -C "{target}" checkout --force "{ref}"') != 0:
                raise RuntimeError(f"failed to checkout {ref}")
    else:
        if run(f'git clone --depth 1 "{repo}" "{target}"') != 0:
            raise RuntimeError(f"failed to clone {repo}")

    return target


def build_env(pkg, orbital_bin, manifest_dir):
    env = os.environ.copy()
    env["ORBITAL"] = orbital_bin
    # Directory containing the manifest (tests/core-packages), so commands can
    # reference shared support files portably, e.g. $CORE_PKG_DIR/support/…
    env["CORE_PKG_DIR"] = manifest_dir
    for k, v in (pkg.get("env") or {}).items():
        env[str(k)] = str(v)
    return env


def run_package(pkg, cache_dir, orbital_bin, manifest_dir, offline, skip_install):
    name = pkg["name"]
    log("")
    log("=" * 72)
    log(f">>> core-package: {name}")
    if pkg.get("description"):
        log(f"    {pkg['description']}")
    log("=" * 72)

    target = ensure_repo(pkg, cache_dir, offline)
    workdir = target
    if pkg.get("subdir"):
        workdir = os.path.join(target, pkg["subdir"])
    env = build_env(pkg, orbital_bin, manifest_dir)

    if not skip_install:
        for cmd in pkg.get("install", []):
            log(f">>> [{name}] install")
            if run(cmd, cwd=workdir, env=env) != 0:
                log(f"    FAIL: install step failed for {name}")
                return False
    else:
        log(f">>> [{name}] skipping install (--skip-install)")

    test_cmds = pkg.get("test", [])
    if not test_cmds:
        log(f"    FAIL: no test commands defined for {name}")
        return False

    ok = True
    for cmd in test_cmds:
        log(f">>> [{name}] test (on Orbital)")
        if run(cmd, cwd=workdir, env=env) != 0:
            log(f"    FAIL: test step failed for {name}")
            ok = False
    if ok:
        log(f"    PASS: {name}")
    return ok


def main():
    repo_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument(
        "--manifest",
        default=os.path.join(repo_root, "tests", "core-packages", "manifest.json"),
    )
    ap.add_argument(
        "--binary",
        default=os.path.join(repo_root, "build", "orbital"),
        help="path to the Orbital binary exposed to test commands as $ORBITAL",
    )
    ap.add_argument(
        "--cache-dir",
        default=os.path.join(repo_root, "tests", "core-packages", ".cache"),
        help="where upstream repos are cloned (git-ignored)",
    )
    ap.add_argument(
        "--package",
        action="append",
        default=[],
        help="only run the named package(s); repeatable",
    )
    ap.add_argument(
        "--offline",
        action="store_true",
        help="do not clone/fetch; reuse existing checkouts",
    )
    ap.add_argument(
        "--skip-install",
        action="store_true",
        help="skip install steps (reuse a previous install)",
    )
    args = ap.parse_args()

    if not os.path.isfile(args.manifest):
        sys.exit(f"ERROR: manifest not found: {args.manifest}")
    with open(args.manifest) as f:
        manifest = json.load(f)

    orbital_bin = os.path.abspath(args.binary)
    if not os.path.isfile(orbital_bin):
        sys.exit(
            f"ERROR: Orbital binary not found: {orbital_bin}\n"
            "Build it first: make build-native"
        )

    packages = manifest.get("packages", [])
    if args.package:
        wanted = set(args.package)
        packages = [p for p in packages if p.get("name") in wanted]
        missing = wanted - {p.get("name") for p in packages}
        if missing:
            sys.exit(f"ERROR: package(s) not in manifest: {', '.join(sorted(missing))}")

    if not packages:
        sys.exit("ERROR: no packages to run (empty manifest or filter matched nothing)")

    cache_dir = os.path.abspath(args.cache_dir)
    manifest_dir = os.path.dirname(os.path.abspath(args.manifest))
    results = {}
    for pkg in packages:
        try:
            results[pkg["name"]] = run_package(
                pkg, cache_dir, orbital_bin, manifest_dir, args.offline, args.skip_install
            )
        except Exception as e:  # noqa: BLE001 - report and keep going
            log(f"    FAIL: {pkg.get('name', '?')}: {e}")
            results[pkg.get("name", "?")] = False

    log("")
    log("=" * 72)
    log(">>> core-package summary")
    for name, ok in results.items():
        log(f"    {'PASS' if ok else 'FAIL'}  {name}")
    log("=" * 72)

    if not all(results.values()):
        sys.exit(1)
    log(f">>> all core-package tests passed ({len(results)} package(s))")


if __name__ == "__main__":
    main()
