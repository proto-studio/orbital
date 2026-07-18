package v8dist

// ModuleVersion is the semantic version of this module and of the GitHub
// Release that hosts the prebuilt V8 artifacts. It mirrors "module_version" in
// manifest.json and is kept in sync by the update workflow.
//
// A compile-time constant is provided so tooling never depends on a floating
// git ref. Note: a MAJOR bump (v2+) also requires a "/vN" suffix on the module
// path in go.mod and on all import paths (Go module rules), so a breaking change
// is more than a tag change.
const ModuleVersion = "v0.1.0"
