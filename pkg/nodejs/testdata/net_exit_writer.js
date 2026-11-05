// Writes to a peer and exits in the same tick, as TestNetWriteBeforeExit's table describes.
// The test runs this in Node or Orbital. It takes the peer's port and a mode.
const net = require('net');

const port = Number(process.env.PARITY_PORT);
const mode = process.env.PARITY_MODE;
const big = Buffer.alloc(64 * 1024, 'x');

const modes = {
  // Nothing has reached the kernel by the time the process exits.
  beforeConnect() {
    const socket = net.connect(port, '127.0.0.1');
    socket.write('lost');
    process.exit(0);
  },
  oneWrite() {
    net.connect(port, '127.0.0.1', function connected() {
      this.write('one');
      process.exit(0);
    });
  },
  severalWrites() {
    net.connect(port, '127.0.0.1', function connected() {
      this.write('a');
      this.write(Buffer.from([0, 0xff]));
      this.write('c');
      process.exit(0);
    });
  },
  endWithData() {
    net.connect(port, '127.0.0.1', function connected() {
      this.end('ended');
      process.exit(0);
    });
  },
  afterEarlierWrite() {
    net.connect(port, '127.0.0.1', function connected() {
      this.write('first', () => {
        this.write('second');
        process.exit(0);
      });
    });
  },
  largeWrite() {
    net.connect(port, '127.0.0.1', function connected() {
      this.write(big);
      process.exit(0);
    });
  },
  corked() {
    net.connect(port, '127.0.0.1', function connected() {
      this.cork();
      this.write('held');
      process.exit(0);
    });
  },
  // The peer in this case is not reading, so this cannot all reach the kernel.
  stalledPeer() {
    net.connect(port, '127.0.0.1', function connected() {
      for (let i = 0; i < 1024; i++) this.write(big);
      process.exit(0);
    });
  },
  exitCode() {
    net.connect(port, '127.0.0.1', function connected() {
      this.write('coded');
      process.exit(3);
    });
  },
};

modes[mode]();
