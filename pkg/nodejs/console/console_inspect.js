// The parts of Node's util.inspect that console.log needs from JavaScript: quoting strings and
// keys, printing Buffers, typed arrays, ArrayBuffers and DataViews, and laying entries out on
// lines. Keys are read here too, because a key holding a
// NUL can't cross into Go as a string. The formatting itself is inspectjs's, which console.go
// passes in as inspectShared. Evaluates to an object of functions.
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
  return {
    quote: inspectShared.quote,
    key: inspectShared.key,
    get: (obj, k) => obj[k],
    view: (value) => inspectShared.view(value),
    binary: (value) => inspectShared.binary(value),
    // json is printf's %j: JSON.stringify, or [Circular] for a value that holds itself. Any
    // other error JSON.stringify throws leaves it answering nothing.
    json: (value) => {
      try {
        return String(JSON.stringify(value));
      } catch (e) {
        return e instanceof TypeError && e.message.split('\n')[0] === circularMessage ? '[Circular]' : null;
      }
    },
    reduce: inspectShared.reduce
  };
})()
