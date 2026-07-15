# Dependencies

This directory contains pre-compiled dependencies for Orbital.

## V8 JavaScript Engine

The `v8/` subdirectory contains compiled V8 static libraries for each supported platform:

```
deps/v8/
├── current/              # Symlink to active platform (gitignored)
├── darwin-arm64/         # macOS Apple Silicon (M1/M2/M3)
│   ├── lib/
│   │   └── libv8_monolith.a
│   └── include/
├── darwin-x64/           # macOS Intel
│   ├── lib/
│   │   └── libv8_monolith.a
│   └── include/
├── linux-arm64/          # Linux ARM64 (AWS Graviton, etc.)
│   ├── lib/
│   │   └── libv8_monolith.a
│   └── include/
└── linux-x64/            # Linux x86_64
    ├── lib/
    │   └── libv8_monolith.a
    └── include/
```

## Building V8

Builds are native — build V8 for the platform you're on:

```bash
make v8-native
```

Or fetch and build the latest stable V8 for this platform:

```bash
make v8-latest
```

Libraries for other platforms are produced by CI on native runners (see
`.github/workflows/update-v8.yml`), not built locally.

## Note on File Sizes

V8 static libraries are large (~50-100MB each). They are committed to git to avoid requiring users to compile V8 themselves (which takes 30-60 minutes).

The `v8-build/` directory (V8 source and build cache) is NOT committed and can be deleted after building.
