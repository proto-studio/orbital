(function() {
	'use strict';

	// Module cache
	const moduleCache = new Map();

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
		'zlib': () => __zlib_module,
		'dns': () => __dns_module,
		'dns/promises': () => __dns_promises_module,
		'readline': () => __readline_module,
		'readline/promises': () => __readline_promises_module,
		'perf_hooks': () => __perf_hooks_module,
		'punycode': () => __punycode_module,
		'sys': () => __sys_module,
		'diagnostics_channel': () => __diagnostics_channel_module,
		'domain': () => __domain_module,
		'repl': () => __repl_module,
		'node:test': () => __test_module,
		'child_process': () => __child_process_module,
	};

	// Check if a native module is registered (via Runtime.RegisterNativeModule)
	function getNativeModule(name) {
		const globalKey = '__native_module_' + name;
		if (typeof globalThis[globalKey] !== 'undefined') {
			return globalThis[globalKey];
		}
		return null;
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
		mod.require.resolve = (request) => resolveModule(request, mod);

		return mod;
	}

	// Get directory from a filename
	function getDirname(filename) {
		if (!filename || filename === '.' || filename === process.cwd()) {
			return process.cwd();
		}
		
		// Check if filename ends with .js, .json, .mjs (it's a file)
		if (filename.endsWith('.js') || filename.endsWith('.json') || filename.endsWith('.mjs')) {
			const lastSlash = filename.lastIndexOf('/');
			if (lastSlash >= 0) {
				return filename.substring(0, lastSlash);
			}
		}
		
		// It's likely a directory path
		return filename;
	}

	// Resolve module path
	function resolveModule(request, parent) {
		// Built-in module
		if (builtinModules[request]) {
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
			// Check for built-in module
			if (builtinModules[request]) {
				return builtinModules[request]();
			}

			// Check for native Go module
			const nativeModule = getNativeModule(request);
			if (nativeModule !== null) {
				return nativeModule;
			}

			// Resolve the full path
			const resolved = resolveModule(request, parentModule);
			if (!resolved) {
				throw new Error('Cannot find module: ' + request);
			}

			// Check cache
			if (moduleCache.has(resolved)) {
				return moduleCache.get(resolved).exports;
			}

			// Load the module
			const mod = createModule(resolved, resolved, parentModule);
			moduleCache.set(resolved, mod);

			if (parentModule) {
				parentModule.children.push(mod);
			}

			// Push to stack
			moduleStack.push(mod);

			try {
				// Read and compile the module
				const source = __requireFile(resolved);
				if (source === null || source === undefined) {
					moduleCache.delete(resolved);
					throw new Error('Cannot find module: ' + request);
				}

				const dirname = resolved.substring(0, resolved.lastIndexOf('/')) || '.';

				// Handle JSON files
				if (resolved.endsWith('.json')) {
					mod.exports = JSON.parse(source);
					mod.loaded = true;
					return mod.exports;
				}

				// Block ES Modules from being required
				if (resolved.endsWith('.mjs')) {
					throw new Error(
						'require() of ES Module ' + resolved + ' not supported.\n' +
						'Instead use dynamic import(): const mod = await import("' + request + '")'
					);
				}

				// Wrap in function to provide module scope
				const wrapper = '(function(exports, require, module, __filename, __dirname) {' +
					source +
					'\n});';

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
				moduleCache.delete(resolved);
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
			return builtinModules.hasOwnProperty(name);
		}
	};
})();
