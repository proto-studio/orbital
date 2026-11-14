// Runs unchanged in Node and in Orbital. os_constants.<platform>.node.json is this script's
// output under Node v20.18.1 on that platform; regenerate it there with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run TestOSConstants_MatchesNode
// Entries keep their order, so a missing, extra or reordered constant shows up too.
const os = require('os');

const finish = globalThis.done || ((value) => console.log(JSON.stringify(value, null, 2)));
const entries = (obj) => Object.entries(obj).map(([name, value]) => `${name}=${value}`);

finish({
  keys: Object.keys(os.constants).filter((k) => ['errno', 'signals', 'priority'].includes(k)),
  errno: entries(os.constants.errno),
  signals: entries(os.constants.signals),
  priority: entries(os.constants.priority),
});
