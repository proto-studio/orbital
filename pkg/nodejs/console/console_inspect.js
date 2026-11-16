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
  // objectPrefix and arrayPrefix are inspect's for a plain object or array at level, which the
  // console prints at util.inspect's default depth of 2.
  const objectPrefix = (value, level) => inspectShared.objectPrefix(value, ctx.depth - level);
  const arrayPrefix = (value, level) => inspectShared.arrayPrefix(value, ctx.depth - level);
  return {
    quote: inspectShared.quote,
    string: (str, level) => inspectShared.string(ctx, str, level),
    more: inspectShared.more,
    // shown is how many of length items to print.
    shown: (length) => Math.min(length, ctx.maxArrayLength),
    view: (value, level) => inspectShared.view(value, ctx, level),
    // binary, collection and scalar describe a value at level.
    binary: (value, level) => inspectShared.binary(value, ctx.maxArrayLength, ctx.depth - level),
    collection: (value, level) => inspectShared.collection(value, ctx.maxArrayLength, ctx.depth - level),
    // scalar answers { text, date } for a Date or RegExp, date telling them apart for coloring.
    scalar: (value, level) => {
      const text = inspectShared.scalar(value, ctx.depth - level);
      return text === null ? null : { text, date: inspectShared.isDate(value) };
    },
    primitive: inspectShared.primitive,
    type: (value) => typeof value,
    // text is printf's %s, or nothing for an object it inspects with a depth of 0.
    text: inspectShared.stringSpec,
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
    // reduceArray and reduceObject lay out the entries of an array or plain object, value, named
    // before its bracket or brace when Node names it, as a subclass is, with ref first.
    reduceArray: (output, level, numbers, ref, value) =>
      inspectShared.reduce(ctx, output, arrayPrefix(value, level) + '[', ']', level, { array: true, numbers, ref }),
    reduceObject: (output, level, value, ref) =>
      inspectShared.reduce(ctx, output, objectPrefix(value, level) + '{', '}', level, { ref }),
    objectPrefix,
    arrayPrefix
  };
})()
