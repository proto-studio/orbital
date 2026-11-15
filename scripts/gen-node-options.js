// Regenerates cmd/orbital/node_options_table.go from the running Node:
//   node --expose-internals scripts/gen-node-options.js 2>/dev/null | gofmt > cmd/orbital/node_options_table.go
const { options, aliases } = require('internal/options').getCLIOptionsInfo();
const { internalBinding } = require('internal/test/binding');
const { types, envSettings } = internalBinding('options');
const typeName = Object.fromEntries(Object.entries(types).map(([k, v]) => [v, k]));
const kinds = { kBoolean: 'optBoolean', kNoOp: 'optNoOp', kV8Option: 'optV8', kString: 'optValue', kStringList: 'optValue', kInteger: 'optValue', kUInteger: 'optValue', kHostPort: 'optValue' };
let out = '// Code generated from Node 20.18.1 by scripts/gen-node-options.js; DO NOT EDIT.\n\npackage main\n\n';
out += '// nodeEnvOptions are the options Node 20.18.1 accepts in NODE_OPTIONS, by kind.\nvar nodeEnvOptions = map[string]nodeOptKind{\n';
for (const name of [...options.keys()].sort()) {
  const o = options.get(name);
  if (o.envVarSettings !== envSettings.kAllowedInEnvvar) continue;
  out += `\t${JSON.stringify(name)}: ${kinds[typeName[o.type]]},\n`;
}
out += '}\n\n// nodeOptionAliases are Node 20.18.1\'s option aliases. A key ending in "=" applies\n// only when the option is given a value with "=".\nvar nodeOptionAliases = map[string][]string{\n';
for (const name of [...aliases.keys()].sort()) out += `\t${JSON.stringify(name)}: {${aliases.get(name).map((s) => JSON.stringify(s)).join(', ')}},\n`;
out += '}\n';
process.stdout.write(out);
