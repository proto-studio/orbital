// What was written before process.exit must already be out.
// 0x41 'A', NUL, 0xFF (never valid in UTF-8).
process.stdout.write(Buffer.from([0x41, 0, 0xff]));
console.log('p\0q');
// 0x42 'B', NUL, 0xFE (never valid in UTF-8).
process.stderr.write(Buffer.from([0x42, 0, 0xfe]));
process.exit(0);
