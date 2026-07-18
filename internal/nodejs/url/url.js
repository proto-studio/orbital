(function() {
	'use strict';

	// URLSearchParams class
	class URLSearchParams {
		constructor(init) {
			this._params = [];

			if (typeof init === 'string') {
				if (init.startsWith('?')) {
					init = init.slice(1);
				}
				if (init) {
					init.split('&').forEach(pair => {
						const [key, value] = pair.split('=').map(decodeURIComponent);
						this._params.push([key, value !== undefined ? value : '']);
					});
				}
			} else if (Array.isArray(init)) {
				init.forEach(([key, value]) => {
					this._params.push([String(key), String(value)]);
				});
			} else if (init && typeof init === 'object') {
				if (init instanceof URLSearchParams) {
					init.forEach((value, key) => {
						this._params.push([key, value]);
					});
				} else {
					Object.keys(init).forEach(key => {
						this._params.push([key, String(init[key])]);
					});
				}
			}
		}

		append(name, value) {
			this._params.push([String(name), String(value)]);
		}

		delete(name) {
			this._params = this._params.filter(([key]) => key !== name);
		}

		get(name) {
			const found = this._params.find(([key]) => key === name);
			return found ? found[1] : null;
		}

		getAll(name) {
			return this._params.filter(([key]) => key === name).map(([, value]) => value);
		}

		has(name) {
			return this._params.some(([key]) => key === name);
		}

		set(name, value) {
			let found = false;
			this._params = this._params.filter(([key]) => {
				if (key === name) {
					if (!found) {
						found = true;
						return true;
					}
					return false;
				}
				return true;
			});
			if (found) {
				const idx = this._params.findIndex(([key]) => key === name);
				this._params[idx][1] = String(value);
			} else {
				this._params.push([String(name), String(value)]);
			}
		}

		sort() {
			this._params.sort((a, b) => a[0].localeCompare(b[0]));
		}

		toString() {
			return this._params
				.map(([key, value]) => `${encodeURIComponent(key)}=${encodeURIComponent(value)}`)
				.join('&');
		}

		forEach(callback, thisArg) {
			this._params.forEach(([key, value]) => {
				callback.call(thisArg, value, key, this);
			});
		}

		entries() {
			return this._params[Symbol.iterator]();
		}

		keys() {
			return this._params.map(([key]) => key)[Symbol.iterator]();
		}

		values() {
			return this._params.map(([, value]) => value)[Symbol.iterator]();
		}

		[Symbol.iterator]() {
			return this.entries();
		}

		get size() {
			return this._params.length;
		}
	}

	// URL class
	class URL {
		constructor(url, base) {
			if (base !== undefined) {
				// Resolve relative URL against base
				const baseUrl = typeof base === 'string' ? new URL(base) : base;
				url = URL._resolveRelative(baseUrl, url);
			}

			const parsed = URL._parse(url);
			if (!parsed) {
				throw new TypeError(`Invalid URL: ${url}`);
			}

			this._protocol = parsed.protocol || '';
			this._username = parsed.username || '';
			this._password = parsed.password || '';
			this._hostname = parsed.hostname || '';
			this._port = parsed.port || '';
			this._pathname = parsed.pathname || '/';
			this._search = parsed.search || '';
			this._hash = parsed.hash || '';
			this._slashes = !!parsed.slashes;
			this._searchParams = new URLSearchParams(this._search);
		}

		static _parse(url) {
			// Protocol
			const protocolMatch = url.match(/^([a-zA-Z][a-zA-Z0-9+.-]*:)/);
			if (!protocolMatch) {
				return null;
			}

			const protocol = protocolMatch[1].toLowerCase();
			let rest = url.slice(protocol.length);

			// Authority (//user:pass@host:port)
			let username = '';
			let password = '';
			let hostname = '';
			let port = '';

			// For "special" schemes (http, https, ws, wss, ftp, file) the WHATWG
			// URL parser treats backslashes as forward slashes, so a backslash
			// terminates the authority (e.g. http://google.com\@apple.com has host
			// google.com, not apple.com). Other schemes keep backslashes literal.
			const special = /^(https?|wss?|ftp|file):$/.test(protocol);
			const authorityEnd = special ? /[/\\?#]/ : /[/?#]/;
			const slashes = rest.startsWith('//');

			if (rest.startsWith('//')) {
				rest = rest.slice(2);
				const pathStart = rest.search(authorityEnd);
				const authority = pathStart === -1 ? rest : rest.slice(0, pathStart);
				rest = pathStart === -1 ? '' : rest.slice(pathStart);

				// User info
				const atIndex = authority.lastIndexOf('@');
				let hostPart = authority;
				if (atIndex !== -1) {
					const userInfo = authority.slice(0, atIndex);
					hostPart = authority.slice(atIndex + 1);
					const colonIndex = userInfo.indexOf(':');
					if (colonIndex !== -1) {
						username = decodeURIComponent(userInfo.slice(0, colonIndex));
						password = decodeURIComponent(userInfo.slice(colonIndex + 1));
					} else {
						username = decodeURIComponent(userInfo);
					}
				}

				// Host and port
				// Handle IPv6
				if (hostPart.startsWith('[')) {
					const bracketEnd = hostPart.indexOf(']');
					if (bracketEnd !== -1) {
						hostname = hostPart.slice(0, bracketEnd + 1);
						const portPart = hostPart.slice(bracketEnd + 1);
						if (portPart.startsWith(':')) {
							port = portPart.slice(1);
						}
					}
				} else {
					const colonIndex = hostPart.lastIndexOf(':');
					if (colonIndex !== -1) {
						hostname = hostPart.slice(0, colonIndex).toLowerCase();
						port = hostPart.slice(colonIndex + 1);
					} else {
						hostname = hostPart.toLowerCase();
					}
				}
			}

			// Pathname, search, hash
			let pathname = '/';
			let search = '';
			let hash = '';

			const hashIndex = rest.indexOf('#');
			if (hashIndex !== -1) {
				hash = rest.slice(hashIndex);
				rest = rest.slice(0, hashIndex);
			}

			const searchIndex = rest.indexOf('?');
			if (searchIndex !== -1) {
				search = rest.slice(searchIndex);
				pathname = rest.slice(0, searchIndex) || '/';
			} else {
				pathname = rest || '/';
			}

			return { protocol, username, password, hostname, port, pathname, search, hash, slashes };
		}

		static _resolveRelative(base, relative) {
			if (relative.match(/^[a-zA-Z][a-zA-Z0-9+.-]*:/)) {
				return relative; // Already absolute
			}

			if (relative.startsWith('//')) {
				return base.protocol + relative;
			}

			if (relative.startsWith('/')) {
				return base.origin + relative;
			}

			if (relative.startsWith('?')) {
				return base.origin + base.pathname + relative;
			}

			if (relative.startsWith('#')) {
				return base.origin + base.pathname + base.search + relative;
			}

			// Relative path
			const basePath = base.pathname.slice(0, base.pathname.lastIndexOf('/') + 1);
			return base.origin + basePath + relative;
		}

		get protocol() { return this._protocol; }
		set protocol(value) {
			value = String(value);
			if (!value.endsWith(':')) value += ':';
			this._protocol = value.toLowerCase();
		}

		get username() { return this._username; }
		set username(value) { this._username = encodeURIComponent(value); }

		get password() { return this._password; }
		set password(value) { this._password = encodeURIComponent(value); }

		get hostname() { return this._hostname; }
		set hostname(value) { this._hostname = String(value).toLowerCase(); }

		get port() { return this._port; }
		set port(value) {
			value = String(value);
			const num = parseInt(value, 10);
			if (isNaN(num) || num < 0 || num > 65535) {
				this._port = '';
			} else {
				this._port = String(num);
			}
		}

		get host() {
			return this._port ? `${this._hostname}:${this._port}` : this._hostname;
		}
		set host(value) {
			const colonIndex = value.lastIndexOf(':');
			if (colonIndex !== -1 && !value.includes('[')) {
				this.hostname = value.slice(0, colonIndex);
				this.port = value.slice(colonIndex + 1);
			} else {
				this.hostname = value;
				this._port = '';
			}
		}

		get origin() {
			return `${this._protocol}//${this.host}`;
		}

		get pathname() { return this._pathname; }
		set pathname(value) {
			value = String(value);
			if (!value.startsWith('/')) value = '/' + value;
			this._pathname = value;
		}

		get search() { return this._search; }
		set search(value) {
			value = String(value);
			if (value && !value.startsWith('?')) value = '?' + value;
			this._search = value;
			this._searchParams = new URLSearchParams(value);
		}

		get searchParams() {
			return this._searchParams;
		}

		get hash() { return this._hash; }
		set hash(value) {
			value = String(value);
			if (value && !value.startsWith('#')) value = '#' + value;
			this._hash = value;
		}

		get href() {
			let url = this._protocol;
			if (this._hostname) {
				url += '//';
				if (this._username) {
					url += encodeURIComponent(this._username);
					if (this._password) {
						url += ':' + encodeURIComponent(this._password);
					}
					url += '@';
				}
				url += this.host;
			}
			url += this._pathname;
			// Sync searchParams to search
			const params = this._searchParams.toString();
			if (params) {
				url += '?' + params;
			}
			url += this._hash;
			return url;
		}
		set href(value) {
			const newUrl = new URL(value);
			this._protocol = newUrl._protocol;
			this._username = newUrl._username;
			this._password = newUrl._password;
			this._hostname = newUrl._hostname;
			this._port = newUrl._port;
			this._pathname = newUrl._pathname;
			this._search = newUrl._search;
			this._hash = newUrl._hash;
			this._searchParams = newUrl._searchParams;
		}

		toString() {
			return this.href;
		}

		toJSON() {
			return this.href;
		}
	}

	// Legacy url.parse function
	function parse(urlString, parseQueryString, slashesDenoteHost) {
		const result = {
			protocol: null,
			slashes: null,
			auth: null,
			host: null,
			port: null,
			hostname: null,
			hash: null,
			search: null,
			query: null,
			pathname: null,
			path: null,
			href: urlString
		};

		try {
			// Try to parse as full URL
			const url = new URL(urlString);
			result.protocol = url.protocol;
			result.slashes = url._slashes || null;
			// When an authority ("//") was present the host is a string even if
			// empty (e.g. file:///etc -> host ''); only fall back to null when the
			// URL genuinely had no authority component.
			result.hostname = url._slashes ? url.hostname : (url.hostname || null);
			result.port = url.port || null;
			result.host = url._slashes ? url.host : (url.host || null);
			result.pathname = url.pathname || null;
			result.search = url.search || null;
			result.hash = url.hash || null;
			result.path = (url.pathname || '') + (url.search || '');

			if (url.username || url.password) {
				result.auth = url.username + (url.password ? ':' + url.password : '');
			}

			if (parseQueryString && result.search) {
				const params = new URLSearchParams(result.search);
				result.query = {};
				params.forEach((value, key) => {
					if (result.query[key] !== undefined) {
						if (Array.isArray(result.query[key])) {
							result.query[key].push(value);
						} else {
							result.query[key] = [result.query[key], value];
						}
					} else {
						result.query[key] = value;
					}
				});
			} else {
				result.query = result.search ? result.search.slice(1) : null;
			}
		} catch (e) {
			// Handle non-standard URLs
			let rest = urlString;

			// Hash
			const hashIdx = rest.indexOf('#');
			if (hashIdx !== -1) {
				result.hash = rest.slice(hashIdx);
				rest = rest.slice(0, hashIdx);
			}

			// Search
			const searchIdx = rest.indexOf('?');
			if (searchIdx !== -1) {
				result.search = rest.slice(searchIdx);
				rest = rest.slice(0, searchIdx);
				if (parseQueryString) {
					const params = new URLSearchParams(result.search);
					result.query = {};
					params.forEach((value, key) => {
						result.query[key] = value;
					});
				} else {
					result.query = result.search.slice(1);
				}
			}

			// Protocol
			const protoMatch = rest.match(/^([a-zA-Z][a-zA-Z0-9+.-]*:)/);
			if (protoMatch) {
				result.protocol = protoMatch[1].toLowerCase();
				rest = rest.slice(result.protocol.length);
			}

			// Slashes. Node only treats a host as present after a literal "//"
			// (protocol-relative when slashesDenoteHost is set, or after a slashed
			// protocol). A single leading slash is always a path, never a host, so
			// url.parse('/foo/bar', false, true).host must be null.
			const hasDoubleSlash = rest.startsWith('//');
			if (hasDoubleSlash && (slashesDenoteHost || result.protocol)) {
				result.slashes = true;
				rest = rest.slice(2);

				// Backslashes terminate the authority for special schemes just like
				// they do in the WHATWG parser above; slashesDenoteHost implies an
				// http-style URL, so treat backslashes as delimiters here too.
				const pathStart = rest.search(/[/\\?#]/);
				const hostPart = pathStart === -1 ? rest : rest.slice(0, pathStart);
				rest = pathStart === -1 ? '' : rest.slice(pathStart);

				// Auth
				const atIdx = hostPart.indexOf('@');
				if (atIdx !== -1) {
					result.auth = hostPart.slice(0, atIdx);
					result.host = hostPart.slice(atIdx + 1);
				} else {
					result.host = hostPart;
				}

				// Port
				const colonIdx = result.host.lastIndexOf(':');
				if (colonIdx !== -1) {
					result.hostname = result.host.slice(0, colonIdx);
					result.port = result.host.slice(colonIdx + 1);
				} else {
					result.hostname = result.host;
				}
			}

			result.pathname = rest || null;
			result.path = (result.pathname || '') + (result.search || '');
		}

		// Mirror Node's legacy hostname validation: split the hostname on dots and
		// stop at the first label containing a character invalid in a hostname
		// (e.g. the "'" in "google.com'.bb.com"). The invalid remainder is moved
		// onto the pathname, which is what prevents redirect allow-list bypasses.
		if (result.hostname) {
			const partPattern = /^[+a-z0-9A-Z_-]{0,63}$/;
			const partStart = /^([+a-z0-9A-Z_-]{0,63})(.*)$/;
			const parts = result.hostname.split('.');
			const valid = [];
			let leftover = '';
			for (let i = 0; i < parts.length; i++) {
				if (partPattern.test(parts[i])) {
					valid.push(parts[i]);
					continue;
				}
				const m = parts[i].match(partStart);
				const good = m ? m[1] : '';
				const bad = m ? m[2] : parts[i];
				if (good) valid.push(good);
				const remaining = parts.slice(i + 1);
				leftover = bad + (remaining.length ? '.' + remaining.join('.') : '');
				break;
			}
			if (leftover) {
				result.hostname = valid.join('.') || null;
				result.host = result.hostname
					? (result.port ? result.hostname + ':' + result.port : result.hostname)
					: null;
				result.pathname = leftover + (result.pathname || '');
				result.path = (result.pathname || '') + (result.search || '');
			}
		}

		return result;
	}

	// Legacy url.format function
	function format(urlObject) {
		if (typeof urlObject === 'string') {
			return urlObject;
		}

		let result = '';

		if (urlObject.protocol) {
			result += urlObject.protocol;
		}

		if (urlObject.slashes || (urlObject.protocol && urlObject.protocol.match(/^https?:?$/i))) {
			result += '//';
		}

		if (urlObject.auth) {
			result += urlObject.auth + '@';
		}

		if (urlObject.host) {
			result += urlObject.host;
		} else {
			if (urlObject.hostname) {
				result += urlObject.hostname;
			}
			if (urlObject.port) {
				result += ':' + urlObject.port;
			}
		}

		if (urlObject.pathname) {
			result += urlObject.pathname;
		}

		if (urlObject.search) {
			result += urlObject.search;
		} else if (urlObject.query) {
			if (typeof urlObject.query === 'string') {
				result += '?' + urlObject.query;
			} else {
				result += '?' + new URLSearchParams(urlObject.query).toString();
			}
		}

		if (urlObject.hash) {
			result += urlObject.hash;
		}

		return result;
	}

	// url.resolve function
	function resolve(from, to) {
		return new URL(to, from).href;
	}

	// domainToASCII and domainToUnicode (simplified - no actual punycode)
	function domainToASCII(domain) {
		return domain.toLowerCase();
	}

	function domainToUnicode(domain) {
		return domain;
	}

	// fileURLToPath
	function fileURLToPath(url) {
		if (typeof url === 'string') {
			url = new URL(url);
		}
		if (url.protocol !== 'file:') {
			throw new TypeError('URL must be a file URL');
		}
		let pathname = decodeURIComponent(url.pathname);
		// Windows path handling
		if (pathname.match(/^\/[A-Za-z]:\//)) {
			pathname = pathname.slice(1);
		}
		return pathname;
	}

	// pathToFileURL
	function pathToFileURL(path) {
		let pathname = path.replace(/\\/g, '/');
		if (!pathname.startsWith('/')) {
			pathname = '/' + pathname;
		}
		return new URL('file://' + encodeURI(pathname));
	}

	return {
		URL,
		URLSearchParams,
		parse,
		format,
		resolve,
		domainToASCII,
		domainToUnicode,
		fileURLToPath,
		pathToFileURL
	};
})()
