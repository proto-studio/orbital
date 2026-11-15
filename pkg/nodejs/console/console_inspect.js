// The parts of Node's util.inspect that console.log needs from JavaScript: quoting strings and
// keys, printing BigInts, symbols, Buffers, typed arrays, ArrayBuffers, DataViews, Maps, Sets,
// Dates and RegExps, and laying entries out on lines. Keys are read here too, because a key holding a NUL can't cross into Go as a string.
// The formatting itself is inspectjs's, which console.go passes in as inspectShared. Levels
// count how many objects a value sits inside. Evaluates to an object of functions.
(function () {
  // The message JSON.stringify throws for a value that holds itself.
  let circularMessage = '';
  try {
    const loop = {};
    loop.loop = loop;
    JSON.stringify(loop);
  } catch (e) {
    circularMessage = e.message.split('\n')[0];
  }
  // The console prints with util.inspect's default options.
  const ctx = inspectShared.context();
  // track follows the objects being printed, to find the ones that hold themselves. A console
  // call made while another is printing, by a getter say, gets a tracker of its own; suspended
  // holds the outer calls' trackers meanwhile.
  let track = inspectShared.tracker();
  const suspended = [];
  return {
    quote: inspectShared.quote,
    string: (str, level) => inspectShared.string(ctx, str, level),
    more: inspectShared.more,
    // shown is how many of length items to print.
    shown: (length) => Math.min(length, ctx.maxArrayLength),
    view: (value, level) => inspectShared.view(value, ctx, level),
    binary: (value) => inspectShared.binary(value, ctx.maxArrayLength),
    collection: (value) => inspectShared.collection(value, ctx.maxArrayLength),
    // scalar answers { text, date } for a Date or RegExp, date telling them apart for coloring.
    scalar: (value) => {
      const text = inspectShared.scalar(value);
      return text === null ? null : { text, date: inspectShared.isDate(value) };
    },
    primitive: inspectShared.primitive,
    type: (value) => typeof value,
    // decimal, integer and float are printf's %d, %i and %f.
    decimal: (value) => inspectShared.numeric('d', value),
    integer: (value) => inspectShared.numeric('i', value),
    float: (value) => inspectShared.numeric('f', value),
    field: inspectShared.field,
    keys: inspectShared.keys,
    symbols: inspectShared.symbols,
    // json is printf's %j: JSON.stringify, or [Circular] for a value that holds itself. Any
    // other error JSON.stringify throws leaves it answering nothing.
    json: (value) => {
      try {
        return String(JSON.stringify(value));
      } catch (e) {
        return e instanceof TypeError && e.message.split('\n')[0] === circularMessage ? '[Circular]' : null;
      }
    },
    cut: inspectShared.cut,
    // enter is called before formatting the entries of an object at level.
    enter: (level) => {
      ctx.currentDepth = level;
    },
    // visit and leave are the tracker's: visit answers [Circular *N] for a value being printed
    // already, and leave answers the <ref *N> to pass to reduce as ref. suspend sets the
    // current tracker aside for a fresh one, and resume brings it back.
    visit: (value) => track.visit(value),
    leave: () => track.leave(),
    suspend: () => {
      suspended.push(track);
      track = inspectShared.tracker();
    },
    resume: () => {
      track = suspended.pop();
    },
    reduce: (output, open, close, level, base, ref) => inspectShared.reduce(ctx, output, open, close, level, { base, ref }),
    reduceArray: (output, level, numbers, ref) =>
      inspectShared.reduce(ctx, output, '[', ']', level, { array: true, numbers, ref })
  };
})()
