// Demo re-exports from utils.mjs
import { 
    // From math.mjs (via re-export)
    add, PI, sqrt,
    // From greet.mjs (via re-export)
    hello, goodbye,
    // Renamed export
    WelcomeGreeter,
    // utils.mjs own exports
    formatNumber, repeat
} from './utils.mjs';

console.log('=== Re-export Demo ===\n');

console.log('Math functions (re-exported from math.mjs):');
console.log(`  add(10, 20) = ${add(10, 20)}`);
console.log(`  PI = ${PI}`);
console.log(`  sqrt(81) = ${sqrt(81)}`);

console.log('\nGreeting functions (re-exported from greet.mjs):');
console.log(`  hello("ESM") = ${hello("ESM")}`);
console.log(`  goodbye("CommonJS") = ${goodbye("CommonJS")}`);

console.log('\nRenamed export (Greeter -> WelcomeGreeter):');
const greeter = new WelcomeGreeter("Hi there");
console.log(`  greeter.greet("User") = ${greeter.greet("User")}`);

console.log('\nUtils own exports:');
console.log(`  formatNumber(3.14159, 3) = ${formatNumber(3.14159, 3)}`);
console.log(`  repeat("ES6 ", 3) = ${repeat("ES6 ", 3)}`);

console.log('\n=== Done ===');
