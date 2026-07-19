(function() {
	'use strict';

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
				case 's': return String(arg);
				case 'd': 
				case 'i': return parseInt(arg, 10).toString();
				case 'f': return parseFloat(arg).toString();
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
				case 's': return String(arg);
				case 'd':
				case 'i': return parseInt(arg, 10).toString();
				case 'f': return parseFloat(arg).toString();
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

		const seen = new WeakSet();

		function inspectValue(value, currentDepth) {
			// Handle null and undefined
			if (value === null) return 'null';
			if (value === undefined) return 'undefined';

			const type = typeof value;

			// Primitives
			if (type === 'boolean') return value.toString();
			if (type === 'number') {
				if (Object.is(value, -0)) return '-0';
				return value.toString();
			}
			if (type === 'bigint') return value.toString() + 'n';
			if (type === 'string') return formatString(value);
			if (type === 'symbol') return value.toString();
			if (type === 'function') return formatFunction(value);

			// Objects
			if (type === 'object') {
				// Circular reference check
				if (seen.has(value)) return '[Circular]';

				// Depth check
				if (currentDepth > opts.depth) {
					if (Array.isArray(value)) return '[Array]';
					return '[Object]';
				}

				seen.add(value);

				// Custom inspect
				if (opts.customInspect && typeof value[Symbol.for('nodejs.util.inspect.custom')] === 'function') {
					const result = value[Symbol.for('nodejs.util.inspect.custom')](currentDepth, opts);
					seen.delete(value);
					return typeof result === 'string' ? result : inspectValue(result, currentDepth);
				}

				let result;
				if (Array.isArray(value)) {
					result = formatArray(value, currentDepth);
				} else if (value instanceof Date) {
					result = value.toISOString();
				} else if (value instanceof RegExp) {
					result = value.toString();
				} else if (value instanceof Error) {
					result = formatError(value);
				} else if (value instanceof Map) {
					result = formatMap(value, currentDepth);
				} else if (value instanceof Set) {
					result = formatSet(value, currentDepth);
				} else if (value instanceof Promise) {
					result = 'Promise { <pending> }';
				} else if (ArrayBuffer.isView(value)) {
					result = formatTypedArray(value);
				} else {
					result = formatObject(value, currentDepth);
				}

				seen.delete(value);
				return result;
			}

			return String(value);
		}

		function formatString(str) {
			if (str.length > opts.maxStringLength) {
				str = str.slice(0, opts.maxStringLength) + '...';
			}
			return "'" + str.replace(/'/g, "\\'").replace(/\n/g, '\\n').replace(/\r/g, '\\r').replace(/\t/g, '\\t') + "'";
		}

		function formatFunction(fn) {
			const name = fn.name || '(anonymous)';
			if (fn.toString().startsWith('class')) {
				return `[class ${name}]`;
			}
			return `[Function: ${name}]`;
		}

		function formatArray(arr, depth) {
			if (arr.length === 0) return '[]';
			
			const items = [];
			const maxLen = Math.min(arr.length, opts.maxArrayLength);
			
			for (let i = 0; i < maxLen; i++) {
				items.push(inspectValue(arr[i], depth + 1));
			}
			
			if (arr.length > maxLen) {
				items.push(`... ${arr.length - maxLen} more items`);
			}

			return '[ ' + items.join(', ') + ' ]';
		}

		function formatObject(obj, depth) {
			let keys = Object.keys(obj);
			if (opts.sorted) keys.sort();
			
			if (keys.length === 0) {
				const proto = Object.getPrototypeOf(obj);
				if (proto && proto.constructor && proto.constructor.name !== 'Object') {
					return proto.constructor.name + ' {}';
				}
				return '{}';
			}

			const items = keys.map(key => {
				const value = inspectValue(obj[key], depth + 1);
				return `${key}: ${value}`;
			});

			const proto = Object.getPrototypeOf(obj);
			let prefix = '';
			if (proto && proto.constructor && proto.constructor.name !== 'Object') {
				prefix = proto.constructor.name + ' ';
			}

			return prefix + '{ ' + items.join(', ') + ' }';
		}

		function formatError(err) {
			let str = err.stack || err.toString();
			return str;
		}

		function formatMap(map, depth) {
			if (map.size === 0) return 'Map(0) {}';
			const items = [];
			map.forEach((value, key) => {
				items.push(`${inspectValue(key, depth + 1)} => ${inspectValue(value, depth + 1)}`);
			});
			return `Map(${map.size}) { ${items.join(', ')} }`;
		}

		function formatSet(set, depth) {
			if (set.size === 0) return 'Set(0) {}';
			const items = [];
			set.forEach(value => {
				items.push(inspectValue(value, depth + 1));
			});
			return `Set(${set.size}) { ${items.join(', ')} }`;
		}

		function formatTypedArray(arr) {
			const name = arr.constructor.name;
			const len = arr.length;
			if (len === 0) return `${name}(0) []`;
			const items = Array.from(arr.slice(0, Math.min(len, opts.maxArrayLength)));
			let str = `${name}(${len}) [ ${items.join(', ')}`;
			if (len > opts.maxArrayLength) {
				str += `, ... ${len - opts.maxArrayLength} more items`;
			}
			return str + ' ]';
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
		isNativeError: v => v instanceof Error,
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
