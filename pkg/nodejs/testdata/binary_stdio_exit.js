// What was written before process.exit must already be out.
process.stdout.write(Buffer.from([0x41, 0, 0xff]));
console.log('p\0q');
process.stderr.write(Buffer.from([0x42, 0, 0xfe]));
process.exit(0);
