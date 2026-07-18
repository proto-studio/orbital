(function() {
	'use strict';

	// Module cache. Node exposes this as `require.cache` / `Module._cache`: a
	// plain object keyed by a module's resolved filename. Using a real object
	// (not a Map) is required for Node compatibility — libraries and test suites
	// bust the cache with `delete require.cache[require.resolve(id)]` and probe it
	// with `id in require.cache`, both of which are no-ops against a Map.
	const moduleCache = Object.create(null);
	function cacheHas(key) {
		return Object.prototype.hasOwnProperty.call(moduleCache, key);
	}

	// Built-in modules registry
	const builtinModules = {
		'events': () => __events_module,
		'fs': () => __fs_module,
		'fs/promises': () => __fs_promises_module,
		'path': () => __path_module,
		'buffer': () => __buffer_module,
		'stream': () => __stream_module,
		'stream/promises': () => __stream_promises_module,
		'stream/web': () => __stream_web_module,
		'url': () => __url_module,
		'os': () => __os_module,
		'util': () => __util_module,
		'util/types': () => __util_module.types,
		'crypto': () => __crypto_module,
		'crypto/webcrypto': () => __webcrypto_module,
		'net': () => __net_module,
		'dgram': () => __dgram_module,
		'tls': () => __tls_module,
		'http': () => __http_module,
		'https': () => __https_module,
		'http2': () => __http2_module,
		'timers': () => ({ setTimeout, setInterval, setImmediate, clearTimeout, clearInterval, clearImmediate }),
		'timers/promises': () => __timers_promises_module,
		'string_decoder': () => __string_decoder_module,
		'querystring': () => __querystring_module,
		'assert': () => __assert_module,
		'assert/strict': () => __assert_strict_module,
		'console': () => __console_module,
		'zlib': () => __zlib_module,
		'dns': () => __dns_module,
		'dns/promises': () => __dns_promises_module,
		'readline': () => __readline_module,
		'readline/promises': () => __readline_promises_module,
		'perf_hooks': () => __perf_hooks_module,
		'punycode': () => __punycode_module,
		'sys': () => __sys_module,
		'diagnostics_channel': () => __diagnostics_channel_module,
		'async_hooks': () => __async_hooks_module,
		'worker_threads': () => __worker_threads_module,
		'tty': () => __tty_module,
		'domain': () => __domain_module,
		'repl': () => __repl_module,
		'node:test': () => __test_module,
		'child_process': () => __child_process_module,
		'module': () => globalThis.Module,
		'process': () => globalThis.process,
	};

	// Resolve a request to a builtin registry key, honoring the "node:" prefix.
	// Node.js lets every core module be required with or without the prefix
	// (e.g. require('fs') and require('node:fs')), while a few (like node:test)
	// are conventionally prefixed. Return the matching registry key or null.
	function builtinKey(request) {
		if (Object.prototype.hasOwnProperty.call(builtinModules, request)) {
			return request;
		}
		if (request.startsWith('node:')) {
			const bare = request.slice(5);
			if (Object.prototype.hasOwnProperty.call(builtinModules, bare)) {
				return bare;
			}
		} else {
			const prefixed = 'node:' + request;
			if (Object.prototype.hasOwnProperty.call(builtinModules, prefixed)) {
				return prefixed;
			}
		}
		return null;
	}

	// Check if a native module is registered (via Runtime.RegisterNativeModule)
	function getNativeModule(name) {
		const globalKey = '__native_module_' + name;
		if (typeof globalThis[globalKey] !== 'undefined') {
			return globalThis[globalKey];
		}
		return null;
	}

	// Build a Node-compatible "module not found" error. Node uses the message
	// form `Cannot find module '<request>'` and, crucially, sets
	// err.code === 'MODULE_NOT_FOUND'; libraries branch on that code to treat a
	// missing optional dependency as absent rather than fatal (yargs' config
	// `extends`, optional-require patterns, resolve probes, etc.).
	function moduleNotFoundError(request) {
		const err = new Error("Cannot find module '" + request + "'");
		err.code = 'MODULE_NOT_FOUND';
		return err;
	}

	// Current module stack for nested requires
	const moduleStack = [];

	// Create a module object
	function createModule(id, filename, parent) {
		const mod = {
			id: id,
			filename: filename,
			loaded: false,
			parent: parent,
			children: [],
			exports: {},
			paths: [],
			require: null, // Will be set below
		};

		// Create require function bound to this module
		mod.require = createRequire(mod);
		mod.require.main = globalThis.__mainModule || null;
		mod.require.cache = moduleCache;
		mod.require.resolve = (request) => {
			const resolved = resolveModule(request, mod);
			// Node's require.resolve throws MODULE_NOT_FOUND for unresolvable
			// requests rather than returning a falsy value.
			if (!resolved) {
				throw moduleNotFoundError(request);
			}
			return resolved;
		};

		return mod;
	}

	// Get the directory to resolve a module's requires from. This is always
	// called with a *module filename* (a file, e.g. .../bin/_mocha or
	// .../lib/index.js), so we strip the last path component. Extensionless
	// executables (bin/_mocha, bin/tsc, …) must be handled too, so we do not
	// gate on a known extension. The main module is initialized with cwd (a
	// directory), which is preserved via the sentinel check below.
	function getDirname(filename) {
		if (!filename || filename === '.' || filename === process.cwd()) {
			return process.cwd();
		}

		const lastSlash = filename.lastIndexOf('/');
		if (lastSlash > 0) {
			return filename.substring(0, lastSlash);
		}
		if (lastSlash === 0) {
			return '/';
		}
		// No slash: a bare relative name; resolve from the current directory.
		return process.cwd();
	}

	// Resolve module path
	function resolveModule(request, parent) {
		// Built-in module (with or without the "node:" prefix)
		if (builtinKey(request)) {
			return request;
		}

		// Check for native Go module
		const nativeModule = getNativeModule(request);
		if (nativeModule !== null) {
			return request; // Return the name as the "resolved" path for native modules
		}

		// Get the directory of the parent module
		// Priority: parent.filename > globalThis.module.filename > globalThis.__filename > cwd
		let basePath = process.cwd();
		
		// First check if parent has a valid filename (for nested requires)
		if (parent && parent.filename && parent.filename !== '.' && parent.filename !== process.cwd()) {
			basePath = getDirname(parent.filename);
		} 
		// Check the global module object (may have been updated by runCodeWithPath)
		else if (globalThis.module && globalThis.module.filename && 
		         globalThis.module.filename !== '.' && globalThis.module.filename !== process.cwd()) {
			basePath = getDirname(globalThis.module.filename);
		}
		// Fall back to global __filename (set by main script execution)
		else if (globalThis.__filename && globalThis.__filename !== '.') {
			basePath = getDirname(globalThis.__filename);
		}
		// Finally try __dirname directly
		else if (globalThis.__dirname && globalThis.__dirname !== '.') {
			basePath = globalThis.__dirname;
		}

		// Use Go to resolve the path
		return __resolveModule(request, basePath);
	}

	// Create require function for a module
	function createRequire(parentModule) {
		return function require(request) {
			// Check for built-in module (with or without the "node:" prefix)
			const key = builtinKey(request);
			if (key) {
				return builtinModules[key]();
			}

			// Check for native Go module
			const nativeModule = getNativeModule(request);
			if (nativeModule !== null) {
				return nativeModule;
			}

			// Resolve the full path
			const resolved = resolveModule(request, parentModule);
			if (!resolved) {
				throw moduleNotFoundError(request);
			}

			// Check cache
			if (cacheHas(resolved)) {
				return moduleCache[resolved].exports;
			}

			// Load the module
			const mod = createModule(resolved, resolved, parentModule);
			moduleCache[resolved] = mod;

			if (parentModule) {
				parentModule.children.push(mod);
			}

			// Push to stack
			moduleStack.push(mod);

			try {
				// Read and compile the module
				let source = __requireFile(resolved);
				if (source === null || source === undefined) {
					delete moduleCache[resolved];
					throw moduleNotFoundError(request);
				}

				const dirname = resolved.substring(0, resolved.lastIndexOf('/')) || '.';

				// Strip a UTF-8 BOM if present (Node does this for all files).
				if (source.charCodeAt(0) === 0xFEFF) {
					source = source.slice(1);
				}

				// Handle JSON files
				if (resolved.endsWith('.json')) {
					mod.exports = JSON.parse(source);
					mod.loaded = true;
					return mod.exports;
				}

				// Strip a leading shebang line (e.g. "#!/usr/bin/env node") from
				// JS modules before wrapping. Executables published to npm (mocha's
				// bin/cli.js, .bin shims, etc.) are frequently require()'d, and the
				// "#!" is not valid inside the module function wrapper. The newline
				// is preserved so line numbers in stack traces stay correct.
				if (source.charCodeAt(0) === 0x23 && source.charCodeAt(1) === 0x21) {
					const nl = source.indexOf('\n');
					source = nl === -1 ? '' : source.slice(nl);
				}

				// Block ES Modules from being required
				if (resolved.endsWith('.mjs')) {
					throw new Error(
						'require() of ES Module ' + resolved + ' not supported.\n' +
						'Instead use dynamic import(): const mod = await import("' + request + '")'
					);
				}

				// Wrap in function to provide module scope. The trailing
				// sourceURL makes stack traces reference the real file path
				// instead of an anonymous eval frame.
				const wrapper = '(function(exports, require, module, __filename, __dirname) {' +
					source +
					'\n})\n//# sourceURL=' + resolved;

				// Compile and run
				const compiledWrapper = (0, eval)(wrapper);

				compiledWrapper.call(
					mod.exports,
					mod.exports,
					mod.require,
					mod,
					resolved,
					dirname
				);

				mod.loaded = true;
			} catch (e) {
				delete moduleCache[resolved];
				throw e;
			} finally {
				moduleStack.pop();
			}

			return mod.exports;
		};
	}

	// Create the main module
	const cwd = process.cwd();
	const mainModule = createModule(cwd, cwd, null);
	globalThis.__mainModule = mainModule;

	// Export require globally
	globalThis.require = mainModule.require;
	globalThis.module = mainModule;
	globalThis.exports = mainModule.exports;

	// Make Module available
	globalThis.Module = {
		_cache: moduleCache,
		_extensions: {
			'.js': function(module, filename) {
				const content = __requireFile(filename);
				module._compile(content, filename);
			},
			'.json': function(module, filename) {
				const content = __requireFile(filename);
				module.exports = JSON.parse(content);
			}
		},
		builtinModules: Object.keys(builtinModules),
		createRequire: function(filename) {
			const mod = createModule(filename, filename, null);
			return mod.require;
		},
		isBuiltin: function(name) {
			return builtinKey(name) !== null;
		},
		// Register ESM loader customization hooks. The hooks themselves run in
		// an isolated loader realm (see internal/nodejs/esm/hooks.go); this only
		// forwards the specifier + parent url. `parentURL` may be a string/URL
		// or an options object ({ parentURL, data }); `data`/`transferList` are
		// accepted for API compatibility.
		register: function(specifier, parentURL, options) {
			let parent = parentURL;
			if (parentURL !== null && typeof parentURL === 'object' && !(parentURL instanceof URL)) {
				options = parentURL;
				parent = options.parentURL;
			}
			if (parent instanceof URL) parent = parent.href;
			if (typeof __esmRegister !== 'function') {
				throw new Error('module.register is not supported in this context');
			}
			return __esmRegister(String(specifier), parent ? String(parent) : '');
		}
	};

	// In Node, `require('module')` returns the Module constructor, which also
	// exposes itself as `Module.Module`. Mirror that so both
	// `const M = require('module')` and `const { Module } = require('module')`
	// work.
	globalThis.Module.Module = globalThis.Module;
})();
