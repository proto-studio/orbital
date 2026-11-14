// The parts of Node's util.inspect that console.log needs from JavaScript: quoting strings and
// keys, printing Buffers, typed arrays, ArrayBuffers and DataViews, and laying entries out on
// lines. Keys are read here too, because a key holding a NUL can't cross into Go as a string.
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
  return {
    quote: inspectShared.quote,
    key: inspectShared.key,
    get: (obj, k) => obj[k],
    string: (str, level) => inspectShared.string(ctx, str, level),
    more: inspectShared.more,
    // shown is how many of length items to print.
    shown: (length) => Math.min(length, ctx.maxArrayLength),
    view: (value, level) => inspectShared.view(value, ctx, level),
    binary: (value) => inspectShared.binary(value, ctx.maxArrayLength),
    field: inspectShared.field,
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
    reduce: (output, open, close, level, base) => inspectShared.reduce(ctx, output, open, close, level, { base }),
    reduceArray: (output, level, numbers) =>
      inspectShared.reduce(ctx, output, '[', ']', level, { array: true, numbers })
  };
})()
