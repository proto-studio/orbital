// Strings reach Go as C strings, which end at the first NUL, so path.join('/x', 'a\0b') gave
// '/x/a'. Node keeps the NUL, and fs then refuses the path. Each NUL crosses as a private-use
// character followed by '0' and comes back; that character itself crosses doubled. No path
// operation splits or changes either character, so the pair arrives intact.
(function () {
  const path = globalThis.__path_module;
  if (!path) return;
  const MARK = '';
  const hide = (v) => (typeof v === 'string' && (v.includes('\0') || v.includes(MARK))
    ? v.replace(//g, MARK + MARK).replace(/\0/g, MARK + '0')
    : v);
  const show = (v) => (typeof v === 'string' && v.includes(MARK)
    ? v.replace(/([0])/g, (_, c) => (c === '0' ? '\0' : MARK))
    : v);
  // format reads its fields however the object provides them, inherited or through getters,
  // and parse's result has exactly these fields.
  const FIELDS = ['root', 'dir', 'base', 'ext', 'name'];
  const mapFields = (obj, fn) => {
    if (!obj || typeof obj !== 'object') return obj;
    const out = {};
    for (const key of FIELDS) {
      const value = obj[key];
      if (value !== undefined) out[key] = fn(value);
    }
    return out;
  };
  for (const name of ['basename', 'dirname', 'extname', 'join', 'resolve', 'normalize',
    'isAbsolute', 'relative', 'parse', 'format']) {
    const original = path[name];
    if (typeof original !== 'function') continue;
    path[name] = function () {
      // A suffix is matched against the restored name, since its last character could
      // otherwise remove half of an encoded NUL.
      // Go's Base gives '.' for an empty path, where Node gives ''.
      if (name === 'basename' && arguments[0] === '') return '';
      if (name === 'basename' && arguments.length > 1 && typeof arguments[1] === 'string') {
        const input = arguments[0];
        const base = show(original.call(this, hide(input)));
        const suffix = arguments[1];
        // A suffix equal to the whole name is removed only when the path is just that name.
        if (base === suffix) return input === suffix ? '' : base;
        return suffix && base.endsWith(suffix) ? base.slice(0, -suffix.length) : base;
      }
      const args = Array.prototype.map.call(arguments, (a) => (name === 'format' ? mapFields(a, hide) : hide(a)));
      const result = original.apply(this, args);
      return name === 'parse' ? mapFields(result, show) : show(result);
    };
  }
})();
