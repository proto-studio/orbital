module orbital-example/hellov8

go 1.24.0

require proto.zip/studio/orbital v0.0.2

// This example lives inside the orbital repository, so it consumes the library
// from the local checkout. An external consumer would delete this replace line
// and depend on the published module directly.
replace proto.zip/studio/orbital => ../..
