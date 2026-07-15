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

To build V8 for your platform:

```bash
make v8-native
```

To build for a specific platform (cross-compilation):

```bash
make v8 TARGET_OS=linux TARGET_ARCH=arm64
make v8 TARGET_OS=linux TARGET_ARCH=x64
```

To build for all Linux platforms:

```bash
make v8-all-linux
```

## Note on File Sizes

V8 static libraries are large (~50-100MB each). They are committed to git to avoid requiring users to compile V8 themselves (which takes 30-60 minutes).

The `v8-build/` directory (V8 source and build cache) is NOT committed and can be deleted after building.
