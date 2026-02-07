// ES Module: Main entry point demonstrating ES modules

// Named imports
import { add, multiply, PI, square } from './math.mjs';

// Default import
import hello from './greet.mjs';

// Mixed import (default + named)
import { Greeter, goodbye } from './greet.mjs';

// Namespace import
import * as math from './math.mjs';

console.log('=== ES Module Demo ===\n');

// Using named imports
console.log('Named imports from math.mjs:');
console.log(`  add(2, 3) = ${add(2, 3)}`);
console.log(`  multiply(4, 5) = ${multiply(4, 5)}`);
console.log(`  PI = ${PI}`);
console.log(`  square(7) = ${square(7)}`);

// Using default import
console.log('\nDefault import from greet.mjs:');
console.log(`  hello("World") = ${hello("World")}`);

// Using named imports from greet
console.log('\nNamed imports from greet.mjs:');
console.log(`  goodbye("World") = ${goodbye("World")}`);

// Using class
console.log('\nUsing Greeter class:');
const greeter = new Greeter("Welcome");
console.log(`  greeter.greet("Developer") = ${greeter.greet("Developer")}`);

// Using namespace import
console.log('\nNamespace import (* as math):');
console.log(`  math.E = ${math.E}`);
console.log(`  math.sqrt(16) = ${math.sqrt(16)}`);
console.log(`  math.divide(10, 3) = ${math.divide(10, 3)}`);

console.log('\n=== Demo Complete ===');
