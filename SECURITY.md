# Security Policy

## System and Scope

Orbital is a Go JavaScript runtime built on V8. It ships as a Go module, and its
prebuilt V8 libraries ship as release assets: one `v8-<goos>-<goarch>.tar.gz` per
target, pinned by sha256 in `internal/v8dist/manifest.json`. `internal/v8dist`
downloads an archive from the GitHub Release for the pinned module version and
refuses it if its sha256 doesn't match the manifest.

This policy covers two areas:

- **The runtime** (`pkg/`, `cmd/`): the APIs an embedder uses to run JavaScript,
  including the sandbox described in the README.
- **The build and release pipeline** (`.github/`, `scripts/`, `Makefile`,
  `internal/v8dist/`): how the V8 libraries and the glue are built, pinned, tested
  and published.

## Threat Model and Trust Boundaries

### Actors

| Actor | Authority |
| --- | --- |
| Script author | Supplies the JavaScript an embedder runs. May be untrusted. |
| Embedder | Chooses the `runtime.Config`: filesystem root, network policy, system info. Trusted. |
| Fork contributor | Opens pull requests from a fork. Untrusted. Cannot push to this repository, add labels, or reach its secrets. |
| Maintainer | Has write access to this repository. Trusted. |
| Consumer | Builds a Go program against a released module and downloads its pinned V8 archives. |

Maintainers receive security training and undergo background checks before they
are given write access. Write access lets a maintainer push any branch except
`main`, including `automated/update-v8-*`, and edit workflows on those branches.

### Boundaries

- **Script to host.** A script must not get past the filesystem root, network
  policy or system info the embedder configured.
- **Fork to repository.** Nothing a fork contributor controls may run with this
  repository's write token or secrets, or change what is built, pinned or released.
- **Branch to `main`.** Changes reach `main` only through pull requests. Branch
  protection applies to administrators too and forbids force pushes and deletion.
- **Release to consumer.** A consumer trusts the archives whose sha256 the released
  manifest records. The manifest names the Actions run that built them, and
  `release.yml` checks that run before publishing.

## Security Invariants

- A released manifest pins archives built by `update-v8.yml` or `ci.yml` in this
  repository, both through `glue.yml`, from the commit the run reports as its head.
- `release.yml` refuses to publish if the Makefile, the V8 build recipes or the glue
  on `main` differ from what the manifest records, or if the pinned run is not one of
  those workflows or its pin job did not succeed.
- `update-v8.yml` reuses a pinned build only when it came from a push run of
  `update-v8.yml`, and nothing that produces the artifacts has changed since.
  Otherwise it does a full build.
- `internal/v8dist` installs an archive only when its sha256 matches the manifest.
- Workflows that run on fork pull requests do not check out and run fork code with
  write permissions or secrets.

## Reportable Findings and Severity Context

Report findings that:

- let a script escape the sandbox an embedder configured;
- let a fork contributor, or anyone else without write access, run code with this
  repository's token or secrets, or influence the archives that get pinned or released;
- let a consumer install an archive whose sha256 does not match the released manifest;
- break one of the invariants above without needing write access;
- remove or weaken an existing protection, even when the change needs write access
  (see "Protections must not regress" below);
- show signs that the accepted risk below is being exploited.

## Out of Scope, Exclusions, and Accepted Risk

**Build and release tampering by a maintainer.** Someone with write access can
change what CI builds. For example, they could push a workflow or script change to an
`automated/update-v8-*` branch, let it pin a build, and revert the change before the
pull request merges, with or without `[skip ci]`. Treat a new way to do this, whose
only precondition is write access, as low severity and an accepted risk. The
compensating controls are below. Where such a finding also needs no write access,
report it normally.

Accepting the risk doesn't mean ignoring it. Reviewers should know this class of
attack and check every pull request for signs that it is being used, and report them
at full severity:

- A branch's history changes a workflow, `.github/actions`, `scripts/`, or the
  Makefile's V8 recipes, and a later commit undoes it, especially when a build was
  pinned in between.
- Commits that use `[skip ci]` or `[rebuild v8]` without a clear reason.
- A manifest whose `source_run_id`, `source_commit`, checksums or `targets` change
  in a commit other than a `Pin V8 artifacts` commit from `github-actions[bot]`, or
  that names a run from a workflow other than `update-v8.yml` or `ci.yml`.
- A workflow added in one commit and deleted in a later one.

**Protections must not regress.** The accepted risk covers what a maintainer can
already do. It does not cover weakening the controls that limit it. Report any
change that removes or loosens an invariant above, or a check in `release.yml`,
`update-v8.yml`, `glue.yml`, `ci.yml` or `internal/v8dist`, at full severity even
though making it needs write access. Examples are dropping a comparison from the
reuse check, accepting more workflows as pin sources, matching the pin job less
strictly, skipping checksum verification, or giving fork pull requests write
permissions or secrets. A change that weakens a protection needs a stated reason in
its pull request.

## Known Limitations and Compensating Controls

- Pull requests into `main` need no approving review. A maintainer reviews and merges
  their own changes.
- The checks in `release.yml` and `update-v8.yml` compare against the commit the
  pinned run started from. They can't see changes that were made on a branch and
  reverted before the next check ran.
- Compensating controls for maintainer tampering: maintainers receive security
  training and undergo background checks; branch history and Actions run logs record
  every commit and build, including reverted ones; and `release.yml` will not publish
  a version whose tag already exists.

Revisit this policy when workflows, the release process, who gets write access, or the
runtime's sandbox change.
