// The parts of Node's util.inspect that console.log needs from JavaScript: quoting strings and
// keys, and printing Buffers and typed arrays. Keys are read here too, because a key holding a
// NUL can't cross into Go as a string. The formatting itself is inspectjs's, which console.go
// passes in as inspectShared. Evaluates to an object of functions.
(function () {
  return {
    quote: inspectShared.quote,
    key: inspectShared.key,
    get: (obj, k) => obj[k],
    view: (value) => inspectShared.view(value)
  };
})()
