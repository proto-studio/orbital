// Writes more than a pipe holds, so a short write must not lose the rest.
const large = Buffer.alloc(3 * 1024 * 1024);
for (let i = 0; i < large.length; i++) large[i] = (i * 31) % 256;
process.stdout.write(large);
process.stdout.write(large.toString('latin1'), 'latin1');
