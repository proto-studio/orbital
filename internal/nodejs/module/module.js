(function() {
	'use strict';

	// Module cache
	const moduleCache = new Map();

	// Built-in modules registry
	const builtinModules = {
		'events': () => __events_module,
		'fs': () => __fs_module,
		'path': () => __path_module,
		'buffer': () => __buffer_module,
		'stream': () => __stream_module,
		'url': () => __url_module,
		'os': () => __os_module,
		'util': () => __util_module,
		'crypto': () => __crypto_module,
		'http': () => __http_module,
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
		let basePath = process.cwd();
		
		// First check if parent has a valid filename
		if (parent && parent.filename && parent.filename !== '.' && parent.filename !== process.cwd()) {
			// Check if filename ends with .js or .json (it's a file)
			if (parent.filename.endsWith('.js') || parent.filename.endsWith('.json') || parent.filename.endsWith('.mjs')) {
				const lastSlash = parent.filename.lastIndexOf('/');
				if (lastSlash >= 0) {
					basePath = parent.filename.substring(0, lastSlash);
				}
			} else {
				// It's likely a directory path
				basePath = parent.filename;
			}
		} else if (globalThis.__filename && globalThis.__filename !== '.') {
			// Fall back to global __filename (set by main script execution)
			const lastSlash = globalThis.__filename.lastIndexOf('/');
			if (lastSlash >= 0) {
				basePath = globalThis.__filename.substring(0, lastSlash);
			}
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
