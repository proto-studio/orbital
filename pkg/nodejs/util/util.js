(function() {
	'use strict';

	// Native error brand. instanceof Error is false after
	// Object.setPrototypeOf(err, null); Node still treats that value as an
	// error (util.types.isNativeError). Error.isError is the standard check
	// when this V8 provides it.
	function isBrandedError(v) {
		if (v === null || (typeof v !== 'object' && typeof v !== 'function')) return false;
		if (typeof Error.isError === 'function') {
			try { return Error.isError(v); } catch (e) { return false; }
		}
		try {
			if (v instanceof Error) return true;
		} catch (e) {}
		try {
			if (v[Symbol.toStringTag] != null) return false;
			return Object.prototype.toString.call(v) === '[object Error]';
		} catch (e) {
			return false;
		}
	}

	// What inspect formats as an error: Node's isError, a native error or any
	// object inheriting from Error.prototype, such as the error a worker's
	// exception becomes in its parent.
	function isErrorForInspect(v) {
		if (isBrandedError(v)) return true;
		try {
			return v instanceof Error;
		} catch (e) {
			return false;
		}
	}

	// %s prints what inspectjs's stringSpec gives, and inspects any other object with a depth
	// of 0, as Node does (so an Error keeps its stack).
	function formatStringSpecifier(arg, inspectOptions) {
		const text = inspectShared.stringSpec(arg);
		if (text !== null) return text;
		return inspect(arg, Object.assign({}, inspectOptions, { compact: 3, colors: false, depth: 0 }));
	}

	// util.format - printf-like string formatting
	function format(fmt, ...args) {
		// Node returns '' for util.format() with no arguments (whereas
		// util.format(undefined) inspects to 'undefined').
		if (arguments.length === 0) return '';
		if (typeof fmt !== 'string') {
			return [fmt, ...args].map(v => inspect(v)).join(' ');
		}

		let i = 0;
		let str = fmt.replace(/%([sdifjoOc%])/g, (match, type) => {
			if (type === '%') return '%';
			if (i >= args.length) return match;
			
			const arg = args[i++];
			switch (type) {
				case 's': return formatStringSpecifier(arg);
				case 'd': 
				case 'i':
				case 'f': return inspectShared.numeric(type, arg);
				case 'j': return JSON.stringify(arg);
				case 'o':
				case 'O': return inspect(arg);
				case 'c': return ''; // CSS - ignore in Node
				default: return match;
			}
		});

		// Append remaining args
		while (i < args.length) {
			str += ' ' + inspect(args[i++]);
		}

		return str;
	}

	// util.formatWithOptions
	function formatWithOptions(inspectOptions, fmt, ...args) {
		// As with format(), no format arguments (only the options object) is ''.
		if (arguments.length <= 1) return '';
		if (typeof fmt !== 'string') {
			return [fmt, ...args].map(v => inspect(v, inspectOptions)).join(' ');
		}

		let i = 0;
		let str = fmt.replace(/%([sdifjoOc%])/g, (match, type) => {
			if (type === '%') return '%';
			if (i >= args.length) return match;
			
			const arg = args[i++];
			switch (type) {
				case 's': return formatStringSpecifier(arg, inspectOptions);
				case 'd':
				case 'i':
				case 'f': return inspectShared.numeric(type, arg);
				case 'j': return JSON.stringify(arg);
				case 'o':
				case 'O': return inspect(arg, inspectOptions);
				case 'c': return '';
				default: return match;
			}
		});

		while (i < args.length) {
			str += ' ' + inspect(args[i++], inspectOptions);
		}

		return str;
	}

	// util.inspect - object inspection
	function inspect(obj, options) {
		const opts = Object.assign({
			showHidden: false,
			depth: 2,
			colors: false,
			customInspect: true,
			maxArrayLength: 100,
			maxStringLength: 10000,
			breakLength: 80,
			compact: 3,
			sorted: false,
			getters: false
		}, options);

		// layout holds the options for laying out entries, and the level of the object whose
		// entries were formatted last. inspectShared is the formatting util shares with
		// console; util.go passes it in.
		const layout = inspectShared.context(opts);
		// track follows the objects being formatted, to find the ones that hold themselves;
		// tracked counts them. reduce lays out the entries of the one visited last, which is
		// done with them, so it leaves it, putting its <ref *N> first.
		const track = inspectShared.tracker();
		let tracked = 0;
		const leave = () => {
			tracked--;
			return track.leave();
		};
		const reduce = (output, open, close, level, extra) =>
			inspectShared.reduce(layout, output, open, close, level, { ...extra, ref: leave() });

		function inspectValue(value, currentDepth) {
			// Handle null and undefined
			if (value === null) return 'null';
			if (value === undefined) return 'undefined';

			const type = typeof value;

			// Primitives
			if (type === 'boolean') return value.toString();
			if (type === 'string') return inspectShared.string(layout, value, currentDepth);
			if (type === 'function') return formatFunction(value);
			if (type !== 'object') return inspectShared.primitive(value);

			// Objects
			if (type === 'object') {
				// As in Node, a custom inspector runs before value counts as being formatted, so
				// a replacement that holds value formats it afresh. One that answers value
				// itself leaves it to be formatted as usual.
				if (opts.customInspect && typeof value[Symbol.for('nodejs.util.inspect.custom')] === 'function') {
					const result = value[Symbol.for('nodejs.util.inspect.custom')](currentDepth, opts);
					if (result !== value) return typeof result === 'string' ? result : inspectValue(result, currentDepth);
				}
				const circular = track.visit(value);
				if (circular !== null) return circular;
				const mine = ++tracked;
				try {
					return formatObjectValue(value, currentDepth);
				} finally {
					// Left here unless reduce laid it out.
					if (tracked === mine) leave();
				}
			}

			return String(value);
		}

		// formatObjectValue formats value, an object at currentDepth.
		function formatObjectValue(value, currentDepth) {
			// Errors are formatted before the depth cutoff. Node still
			// prints the stack of a nested Error; only its extra fields
			// are dropped once the depth is exceeded.
			if (isErrorForInspect(value)) return formatError(value, currentDepth);

			// Past the depth limit, a value with entries is cut short.
			if (currentDepth > opts.depth) {
				const cut = inspectShared.cut(value);
				if (cut !== null) return cut;
			}

			// Described after the custom inspector has had its chance, which may make
			// describing it unnecessary.
			// How many more levels value may print, which bounds how its prototype is described.
			const budget = layout.depth - currentDepth;
			const bin = inspectShared.binary(value, layout.maxArrayLength, budget);
			const coll = bin === null ? inspectShared.collection(value, layout.maxArrayLength, budget) : null;
			const scalar = coll === null ? inspectShared.scalar(value, budget) : null;
			if (Array.isArray(value)) return formatArray(value, currentDepth);
			if (scalar !== null) return formatScalar(value, scalar, currentDepth);
			if (coll !== null) return formatCollection(value, coll, currentDepth);
			if (value instanceof Promise) return 'Promise { <pending> }';
			if (bin !== null) return formatBinary(value, bin, currentDepth);
			if (ArrayBuffer.isView(value)) return formatView(value, currentDepth);
			return formatObject(value, currentDepth);
		}

		function formatFunction(fn) {
			const name = fn.name || '(anonymous)';
			if (fn.toString().startsWith('class')) {
				return `[class ${name}]`;
			}
			return `[Function: ${name}]`;
		}

		function formatArray(arr, depth) {
			const prefix = inspectShared.arrayPrefix(arr, layout.depth - depth);
			// An empty array prints as [] unless it has keys. Otherwise the keys are listed after
			// the elements are read, as Node does, since listing them can run a proxy's trap.
			// Whether it was empty is kept: formatting an element may change its length.
			const empty = arr.length === 0;
			let extra = [];
			if (empty) {
				extra = inspectShared.extraKeys(arr);
				if (extra.length === 0) return prefix + '[]';
			}
			layout.currentDepth = depth;

			const items = [];
			const maxLen = Math.min(arr.length, Math.max(0, layout.maxArrayLength));

			// Each element is read once; whether they're all numbers decides how columns align.
			let numbers = true;
			for (let i = 0; i < maxLen; i++) {
				const element = arr[i];
				numbers = numbers && (typeof element === 'number' || typeof element === 'bigint');
				items.push(inspectValue(element, depth + 1));
			}

			if (arr.length > maxLen) {
				items.push(inspectShared.more(arr.length - maxLen));
			}
			const elements = items.length;
			if (!empty) extra = inspectShared.extraKeys(arr);
			for (const key of extra) items.push(formatProperty(arr, key, depth, true));
			sortOutput(items, elements);

			return reduce(items, prefix + '[', ']', depth, { array: true, numbers, value: arr, shown: maxLen });
		}

		// formatView prints a typed array or Buffer with its keys after its elements.
		function formatView(value, depth) {
			// Formatted after view reads the elements, as Node does; then done with its entries,
			// the value is left here, its <ref *N> going first. Like the elements, the keys count
			// toward the levels of entries formatted only when there are some.
			const fields = () => {
				const keys = inspectShared.extraKeys(value);
				if (keys.length > 0) layout.currentDepth = depth;
				const list = keys.map(key => formatProperty(value, key, depth, true));
				sortOutput(list);
				return list;
			};
			return inspectShared.view(value, layout, depth, fields, leave);
		}

		// sortOutput sorts the entries of output from start on, as Node does with the sorted
		// option: every entry of an object, Map or Set, and an array's keys after its elements.
		function sortOutput(output, start = 0) {
			if (!opts.sorted || output.length - start < 2) return;
			const sorted = output.slice(start).sort(typeof opts.sorted === 'function' ? opts.sorted : undefined);
			// Copied back one by one, as spreading them into splice would pass one argument each.
			for (let i = 0; i < sorted.length; i++) output[start + i] = sorted[i];
		}

		function formatObject(obj, depth) {
			const keys = inspectShared.keys(obj);
			const prefix = inspectShared.objectPrefix(obj, layout.depth - depth);
			if (keys.length === 0) return prefix + '{}';

			layout.currentDepth = depth;
			const items = keys.map(key => formatProperty(obj, key, depth));
			sortOutput(items);
			return reduce(items, prefix + '{', '}', depth);
		}

		// formatBinary prints an ArrayBuffer or DataView: what inspectShared.binary describes
		// for it, then its own keys.
		function formatBinary(value, bin, depth) {
			layout.currentDepth = depth;
			const keys = bin.keys.concat(inspectShared.keys(value));
			const output = bin.entries.concat(keys.map(key => {
				const f = inspectShared.field(value, key);
				return inspectShared.entry(layout, f.name, 'label' in f ? f.label : inspectValue(f.value, depth + 1));
			}));
			// Node sorts every entry, the ones binary describes included.
			sortOutput(output);
			return reduce(output, bin.name + ' {', '}', depth);
		}

		function errorCtorName(err) {
			let obj = err;
			while (obj) {
				let desc;
				try { desc = Object.getOwnPropertyDescriptor(obj, 'constructor'); }
				catch (e) { desc = undefined; }
				if (desc && typeof desc.value === 'function' && desc.value.name !== '') {
					try {
						if (err instanceof desc.value) return String(desc.value.name);
					} catch (e) {}
				}
				try { obj = Object.getPrototypeOf(obj); }
				catch (e) { break; }
			}
			return null;
		}

		function nullProtoFallback(stack) {
			const header = /^([A-Z][A-Za-z0-9_ \[\]()-]*)(?::|\n {4}at)/.exec(stack);
			if (header) return header[1];
			const only = /^([A-Za-z0-9_-]*Error)$/.exec(stack);
			if (only) return only[1];
			return '';
		}

		// Rewrite "Error: msg" to "MyErr [Error]: msg" when the constructor
		// name and err.name disagree, and to "[Error: null prototype]: msg"
		// when the prototype was removed. Matches Node's inspect.
		function improveErrorStack(stack, name, ctor) {
			let len = name.length;
			const regular = name.endsWith('Error') && stack.startsWith(name) &&
				(stack.length === len || stack[len] === ':' || stack[len] === '\n');
			if (ctor !== null && !regular) return stack;

			let prefix = ctor;
			if (ctor === null) {
				const fallback = nullProtoFallback(stack);
				if (fallback) {
					len = fallback.length;
					prefix = '[' + fallback + ': null prototype]';
				} else {
					len = 0;
					prefix = '[Error: null prototype]';
				}
			}
			if (name === prefix) return stack;
			if (prefix.includes(name)) {
				return len === 0 ? prefix + ': ' + stack : prefix + stack.slice(len);
			}
			return prefix + ' [' + name + ']' + stack.slice(len);
		}

		function formatError(err, depth) {
			const name = err.name != null ? String(err.name) : 'Error';
			const message = err.message != null ? String(err.message) : '';
			const rawStack = err.stack ? String(err.stack) : '';
			let stack = rawStack || (name + (message ? ': ' + message : ''));
			const ctor = errorCtorName(err);
			stack = improveErrorStack(stack, name, ctor);

			const from = message && stack.indexOf(message) >= 0
				? stack.indexOf(message) + message.length
				: 0;
			if (stack.indexOf('\n    at', from) === -1) {
				stack = '[' + stack + ']';
			}

			let keys = [];
			try { keys = inspectShared.keys(err); } catch (e) { keys = []; }
			keys = keys.filter(key => {
				if (key !== 'name' && key !== 'message' && key !== 'stack') return true;
				if (!rawStack) return true;
				const prop = err[key];
				if (prop == null) return true;
				const text = String(prop);
				return text === '' || rawStack.indexOf(text) === -1;
			});

			const hidden = [];
			try {
				if (keys.indexOf('cause') === -1 && 'cause' in err) hidden.push('cause');
			} catch (e) {}
			try {
				if (keys.indexOf('errors') === -1 && Array.isArray(err.errors)) hidden.push('errors');
			} catch (e) {}

			if (keys.length === 0 && hidden.length === 0) return stack;
			// Past the depth limit, an Error with properties to print shows its constructor's
			// name alone.
			if (depth > opts.depth) return ctor === null ? '[Object: null prototype]' : `[${ctor}]`;

			layout.currentDepth = depth;
			const parts = [];
			for (const key of keys) {
				parts.push(formatProperty(err, key, depth));
			}
			for (const key of hidden) {
				parts.push(inspectShared.entry(layout, '[' + key + ']', inspectValue(err[key], depth + 1)));
			}
			sortOutput(parts);

			return reduce(parts, '{', '}', depth, { base: stack });
		}

		// formatProperty prints key of obj, an object at depth: an accessor as a label, which with
		// the getters option holds what the getter returns.
		function formatProperty(obj, key, depth, array = false) {
			const f = inspectShared.field(obj, key);
			if (!('label' in f)) return inspectShared.entry(layout, f.name, inspectValue(f.value, depth + 1), array);
			const desc = Object.getOwnPropertyDescriptor(obj, key);
			const getters = opts.getters;
			if (desc.get === undefined || !(getters === true ||
					(getters === 'get' && desc.set === undefined) ||
					(getters === 'set' && desc.set !== undefined))) {
				return inspectShared.entry(layout, f.name, f.label, array);
			}
			// The label without its brackets.
			const label = f.label.slice(1, -1);
			let value;
			try {
				const got = desc.get.call(obj);
				value = got !== null && typeof got === 'object'
					? `[${label}] ${inspectValue(got, depth + 1)}`
					: `[${label}: ${inspectValue(got, depth + 1)}]`;
			} catch (e) {
				value = `[${label}: <Inspection threw (${e && e.message})>]`;
			}
			return inspectShared.entry(layout, f.name, value, array);
		}

		// formatKeys prints first, then the own enumerable keys of value, an object at depth,
		// between open and close, with base before them.
		function formatKeys(value, first, open, close, depth, base) {
			layout.currentDepth = depth;
			const output = first.concat(inspectShared.keys(value).map(key => formatProperty(value, key, depth)));
			sortOutput(output);
			return reduce(output, open, close, depth, { base });
		}

		// formatScalar prints a Date or RegExp as text, followed by any keys of its own, which a
		// RegExp past the depth limit leaves out.
		function formatScalar(value, text, depth) {
			if (inspectShared.keys(value).length === 0 || depth > opts.depth) return text;
			return formatKeys(value, [], '{', '}', depth, text);
		}

		// formatCollection prints the entries inspectShared.collection describes for a Map or Set,
		// then its own keys.
		function formatCollection(value, coll, depth) {
			const { entries } = coll;
			if (entries.length === 0 && coll.more === 0 && inspectShared.keys(value).length === 0) return `${coll.name} {}`;
			layout.currentDepth = depth;
			const items = [];
			if (coll.map) {
				for (let i = 0; i < entries.length; i += 2) {
					items.push(`${inspectValue(entries[i], depth + 1)} => ${inspectValue(entries[i + 1], depth + 1)}`);
				}
			} else {
				for (const entry of entries) items.push(inspectValue(entry, depth + 1));
			}
			if (coll.more > 0) items.push(inspectShared.more(coll.more));
			return formatKeys(value, items, `${coll.name} {`, '}', depth, '');
		}

		return inspectValue(obj, 0);
	}

	// Add custom inspect symbol
	inspect.custom = Symbol.for('nodejs.util.inspect.custom');

	// Default inspect options
	inspect.defaultOptions = {
		showHidden: false,
		depth: 2,
		colors: false,
		customInspect: true,
		maxArrayLength: 100,
		maxStringLength: 10000,
		breakLength: 80,
		compact: 3,
		sorted: false,
		getters: false
	};

	// util.promisify
	function promisify(fn) {
		if (typeof fn !== 'function') {
			throw new TypeError('The "original" argument must be of type Function');
		}

		// Check for custom promisified version
		const custom = fn[promisify.custom];
		if (custom) {
			if (typeof custom !== 'function') {
				throw new TypeError('The "util.promisify.custom" property must be of type Function');
			}
			return custom;
		}

		function promisified(...args) {
			return new Promise((resolve, reject) => {
				fn.call(this, ...args, (err, ...values) => {
					if (err) {
						reject(err);
					} else if (values.length === 1) {
						resolve(values[0]);
					} else {
						resolve(values);
					}
				});
			});
		}

		Object.setPrototypeOf(promisified, Object.getPrototypeOf(fn));
		Object.defineProperty(promisified, 'name', { value: fn.name });

		return promisified;
	}

	promisify.custom = Symbol.for('nodejs.util.promisify.custom');

	// util.callbackify
	function callbackify(fn) {
		if (typeof fn !== 'function') {
			throw new TypeError('The "original" argument must be of type Function');
		}

		function callbackified(...args) {
			const callback = args.pop();
			if (typeof callback !== 'function') {
				throw new TypeError('The last argument must be of type Function');
			}

			Promise.resolve(fn.apply(this, args))
				.then(
					result => {
						process.nextTick(callback, null, result);
					},
					err => {
						if (!err) {
							err = new Error('Promise rejected with falsy value');
							err.reason = err;
						}
						process.nextTick(callback, err);
					}
				);
		}

		Object.setPrototypeOf(callbackified, Object.getPrototypeOf(fn));
		Object.defineProperty(callbackified, 'name', { value: fn.name + 'Callbackified' });

		return callbackified;
	}

	// util.inherits
	function inherits(ctor, superCtor) {
		if (ctor === undefined || ctor === null) {
			throw new TypeError('The constructor to "inherits" must not be null or undefined');
		}
		if (superCtor === undefined || superCtor === null) {
			throw new TypeError('The super constructor to "inherits" must not be null or undefined');
		}
		if (superCtor.prototype === undefined) {
			throw new TypeError('The super constructor to "inherits" must have a prototype');
		}

		Object.defineProperty(ctor, 'super_', {
			value: superCtor,
			writable: true,
			configurable: true
		});

		Object.setPrototypeOf(ctor.prototype, superCtor.prototype);
	}

	// util.deprecate
	function deprecate(fn, msg, code) {
		if (typeof fn !== 'function') {
			throw new TypeError('The "fn" argument must be of type Function');
		}

		let warned = false;

		function deprecated(...args) {
			if (!warned) {
				warned = true;
				const warning = code ? `[${code}] ${msg}` : msg;
				console.warn('DeprecationWarning:', warning);
			}
			return fn.apply(this, args);
		}

		Object.setPrototypeOf(deprecated, Object.getPrototypeOf(fn));
		Object.defineProperty(deprecated, 'name', { value: fn.name });

		return deprecated;
	}

	// util.types - type checking utilities
	const types = {
		isArray: Array.isArray,
		isArrayBuffer: v => v instanceof ArrayBuffer,
		isArrayBufferView: v => ArrayBuffer.isView(v),
		isAsyncFunction: v => typeof v === 'function' && v.constructor.name === 'AsyncFunction',
		isBigInt64Array: v => v instanceof BigInt64Array,
		isBigUint64Array: v => v instanceof BigUint64Array,
		isBooleanObject: v => v instanceof Boolean,
		isBoxedPrimitive: v => v instanceof Boolean || v instanceof Number || v instanceof String || v instanceof Symbol || (typeof BigInt !== 'undefined' && v instanceof Object && typeof v.valueOf() === 'bigint'),
		isDataView: v => v instanceof DataView,
		isDate: v => v instanceof Date,
		isFloat32Array: v => v instanceof Float32Array,
		isFloat64Array: v => v instanceof Float64Array,
		isFunction: v => typeof v === 'function',
		isGeneratorFunction: v => typeof v === 'function' && v.constructor.name === 'GeneratorFunction',
		isGeneratorObject: v => v && typeof v.next === 'function' && typeof v.throw === 'function',
		isInt8Array: v => v instanceof Int8Array,
		isInt16Array: v => v instanceof Int16Array,
		isInt32Array: v => v instanceof Int32Array,
		isMap: v => v instanceof Map,
		isMapIterator: v => v && v[Symbol.toStringTag] === 'Map Iterator',
		isNativeError: isBrandedError,
		isNumberObject: v => v instanceof Number,
		isPromise: v => v instanceof Promise,
		isRegExp: v => v instanceof RegExp,
		isSet: v => v instanceof Set,
		isSetIterator: v => v && v[Symbol.toStringTag] === 'Set Iterator',
		isSharedArrayBuffer: v => typeof SharedArrayBuffer !== 'undefined' && v instanceof SharedArrayBuffer,
		isStringObject: v => v instanceof String,
		isSymbolObject: v => typeof v === 'object' && v !== null && Object.prototype.toString.call(v) === '[object Symbol]',
		isTypedArray: v => ArrayBuffer.isView(v) && !(v instanceof DataView),
		isUint8Array: v => v instanceof Uint8Array,
		isUint8ClampedArray: v => v instanceof Uint8ClampedArray,
		isUint16Array: v => v instanceof Uint16Array,
		isUint32Array: v => v instanceof Uint32Array,
		isWeakMap: v => v instanceof WeakMap,
		isWeakSet: v => v instanceof WeakSet
	};

	// util.isDeepStrictEqual
	function isDeepStrictEqual(val1, val2) {
		if (Object.is(val1, val2)) return true;
		
		if (typeof val1 !== 'object' || val1 === null ||
		    typeof val2 !== 'object' || val2 === null) {
			return false;
		}

		if (Object.getPrototypeOf(val1) !== Object.getPrototypeOf(val2)) {
			return false;
		}

		if (Array.isArray(val1)) {
			if (!Array.isArray(val2) || val1.length !== val2.length) return false;
			for (let i = 0; i < val1.length; i++) {
				if (!isDeepStrictEqual(val1[i], val2[i])) return false;
			}
			return true;
		}

		if (val1 instanceof Date) {
			return val2 instanceof Date && val1.getTime() === val2.getTime();
		}

		if (val1 instanceof RegExp) {
			return val2 instanceof RegExp && val1.toString() === val2.toString();
		}

		if (val1 instanceof Map) {
			if (!(val2 instanceof Map) || val1.size !== val2.size) return false;
			for (const [key, value] of val1) {
				if (!val2.has(key) || !isDeepStrictEqual(value, val2.get(key))) {
					return false;
				}
			}
			return true;
		}

		if (val1 instanceof Set) {
			if (!(val2 instanceof Set) || val1.size !== val2.size) return false;
			for (const value of val1) {
				if (!val2.has(value)) return false;
			}
			return true;
		}

		const keys1 = Object.keys(val1);
		const keys2 = Object.keys(val2);

		if (keys1.length !== keys2.length) return false;

		for (const key of keys1) {
			if (!keys2.includes(key) || !isDeepStrictEqual(val1[key], val2[key])) {
				return false;
			}
		}

		return true;
	}

	// util.debuglog
	const debugEnv = (typeof process !== 'undefined' && process.env && process.env.NODE_DEBUG) || '';
	const debugLogEnabled = {};

	function debuglog(section, cb) {
		section = section.toUpperCase();
		
		if (debugLogEnabled[section] === undefined) {
			const pattern = new RegExp('\\b' + section + '\\b', 'i');
			debugLogEnabled[section] = pattern.test(debugEnv);
		}

		let logger;
		if (debugLogEnabled[section]) {
			logger = function(...args) {
				const msg = format(...args);
				console.error('%s %d: %s', section, process.pid, msg);
			};
		} else {
			logger = function() {};
		}

		if (cb) {
			cb(logger);
		}

		return logger;
	}

	// util.getSystemErrorName (simplified)
	const errorCodes = {
		'-1': 'EPERM',
		'-2': 'ENOENT',
		'-13': 'EACCES',
		'-17': 'EEXIST',
		'-22': 'EINVAL'
	};

	function getSystemErrorName(err) {
		return errorCodes[String(err)] || 'Unknown system error';
	}

	// util.TextEncoder and util.TextDecoder (use globals if available)
	const TextEncoder = globalThis.TextEncoder;
	const TextDecoder = globalThis.TextDecoder;

	// util.parseArgs - Node's CLI argument parser.
	function parseArgsError(code, message) {
		const err = new TypeError(message);
		err.code = code;
		return err;
	}

	function parseArgs(config) {
		config = config || {};
		const args = config.args !== undefined
			? config.args
			: (typeof process !== 'undefined' && process.argv
				? process.argv.slice(2)
				: []);
		const options = config.options || {};
		const strict = config.strict !== undefined ? config.strict : true;
		const allowPositionals = config.allowPositionals !== undefined
			? config.allowPositionals
			: !strict;
		const allowNegative = config.allowNegative || false;
		const returnTokens = config.tokens || false;

		// Map short flags to their long name for quick lookup.
		const shortMap = {};
		for (const name of Object.keys(options)) {
			const opt = options[name];
			if (opt && opt.short) shortMap[opt.short] = name;
			if (opt && opt.type !== 'string' && opt.type !== 'boolean') {
				throw parseArgsError(
					'ERR_INVALID_ARG_TYPE',
					`options.${name}.type must be "string" or "boolean"`
				);
			}
		}

		const result = { values: { __proto__: null }, positionals: [] };
		const tokens = [];

		function optionType(name) {
			return options[name] && options[name].type === 'string'
				? 'string'
				: 'boolean';
		}

		function storeValue(name, value, inlineValue, rawToken, index) {
			const opt = options[name];
			const type = optionType(name);
			let val = type === 'boolean' ? true : value;
			if (returnTokens) {
				tokens.push({
					kind: 'option',
					name,
					rawName: rawToken,
					index,
					value: type === 'boolean' ? undefined : value,
					inlineValue: type === 'boolean' ? undefined : inlineValue,
				});
			}
			if (opt && opt.multiple) {
				if (!result.values[name]) result.values[name] = [];
				result.values[name].push(val);
			} else {
				result.values[name] = val;
			}
		}

		let i = 0;
		for (; i < args.length; i++) {
			const arg = args[i];
			if (arg === '--') {
				if (returnTokens) tokens.push({ kind: 'option-terminator', index: i });
				for (i++; i < args.length; i++) {
					if (allowPositionals) result.positionals.push(args[i]);
					else if (strict) {
						throw parseArgsError(
							'ERR_PARSE_ARGS_UNEXPECTED_POSITIONAL',
							`Unexpected argument '${args[i]}'. This command does not accept positional arguments`
						);
					}
					if (returnTokens) tokens.push({ kind: 'positional', index: i, value: args[i] });
				}
				break;
			}

			// Long option: --name or --name=value
			if (arg.length > 2 && arg.startsWith('--')) {
				let name = arg.slice(2);
				let inlineValue;
				let hasInline = false;
				const eq = name.indexOf('=');
				if (eq !== -1) {
					inlineValue = name.slice(eq + 1);
					name = name.slice(0, eq);
					hasInline = true;
				}
				let negated = false;
				if (allowNegative && name.startsWith('no-') && !(name in options)) {
					const base = name.slice(3);
					if (base in options && optionType(base) === 'boolean') {
						name = base;
						negated = true;
					}
				}
				if (strict && !(name in options)) {
					throw parseArgsError(
						'ERR_PARSE_ARGS_UNKNOWN_OPTION',
						`Unknown option '--${name}'`
					);
				}
				const type = optionType(name);
				if (type === 'boolean') {
					if (hasInline && strict) {
						throw parseArgsError(
							'ERR_PARSE_ARGS_INVALID_OPTION_VALUE',
							`Option '--${name}' is a boolean and does not take an argument`
						);
					}
					storeValue(name, negated ? false : true, false, `--${arg.slice(2).split('=')[0]}`, i);
				} else {
					let value = inlineValue;
					if (!hasInline) {
						if (i + 1 < args.length) {
							value = args[++i];
						} else if (strict) {
							throw parseArgsError(
								'ERR_PARSE_ARGS_INVALID_OPTION_VALUE',
								`Option '--${name}' argument is missing`
							);
						} else {
							value = undefined;
						}
					}
					storeValue(name, value, hasInline, `--${name}`, i);
				}
				continue;
			}

			// Short option(s): -a, -abc, -avalue, -a=value
			if (arg.length > 1 && arg[0] === '-' && arg !== '-') {
				const body = arg.slice(1);
				for (let j = 0; j < body.length; j++) {
					const short = body[j];
					const name = shortMap[short] || short;
					if (strict && !(name in options)) {
						throw parseArgsError(
							'ERR_PARSE_ARGS_UNKNOWN_OPTION',
							`Unknown option '-${short}'`
						);
					}
					const type = optionType(name);
					if (type === 'string') {
						// Remainder of this token (or next arg) is the value.
						let rest = body.slice(j + 1);
						if (rest.startsWith('=')) rest = rest.slice(1);
						let value;
						if (rest.length > 0) {
							value = rest;
						} else if (i + 1 < args.length) {
							value = args[++i];
						} else if (strict) {
							throw parseArgsError(
								'ERR_PARSE_ARGS_INVALID_OPTION_VALUE',
								`Option '-${short}' argument is missing`
							);
						}
						storeValue(name, value, rest.length > 0, `-${short}`, i);
						break;
					} else {
						storeValue(name, true, false, `-${short}`, i);
					}
				}
				continue;
			}

			// Positional
			if (allowPositionals) {
				result.positionals.push(arg);
			} else if (strict) {
				throw parseArgsError(
					'ERR_PARSE_ARGS_UNEXPECTED_POSITIONAL',
					`Unexpected argument '${arg}'. This command does not accept positional arguments`
				);
			}
			if (returnTokens) tokens.push({ kind: 'positional', index: i, value: arg });
		}

		// Apply defaults for options not seen.
		for (const name of Object.keys(options)) {
			const opt = options[name];
			if (opt && 'default' in opt && !(name in result.values)) {
				result.values[name] = opt.default;
			}
		}

		if (returnTokens) result.tokens = tokens;
		return result;
	}

	return {
		format,
		formatWithOptions,
		inspect,
		promisify,
		callbackify,
		inherits,
		deprecate,
		types,
		isDeepStrictEqual,
		debuglog,
		getSystemErrorName,
		parseArgs,
		TextEncoder,
		TextDecoder
	};
})()
