// CommonJS Module System Demo
// Run from this directory: gnode main.js

console.log("=== CommonJS Module Demo ===\n");

// ─────────────────────────────────────────────────────────────
// Requiring local modules
// ─────────────────────────────────────────────────────────────
console.log("--- Loading Modules ---");

const math = require('./lib/math');
const greet = require('./lib/greet');
const config = require('./config.json');

console.log("Loaded: math, greet, config.json\n");

// ─────────────────────────────────────────────────────────────
// Using the math module
// ─────────────────────────────────────────────────────────────
console.log("--- Math Module ---");
console.log("add(5, 3):", math.add(5, 3));
console.log("subtract(10, 4):", math.subtract(10, 4));
console.log("multiply(6, 7):", math.multiply(6, 7));
console.log("divide(20, 4):", math.divide(20, 4));
console.log("PI:", math.PI);
console.log("circleArea(5):", math.circleArea(5).toFixed(2));

// ─────────────────────────────────────────────────────────────
// Using the greet module
// ─────────────────────────────────────────────────────────────
console.log("\n--- Greet Module ---");
console.log(greet.hello("World"));
console.log(greet.goodbye("GNode"));
console.log(greet.formal("Dr.", "Smith"));

// ─────────────────────────────────────────────────────────────
// Using JSON config
// ─────────────────────────────────────────────────────────────
console.log("\n--- JSON Config ---");
console.log("App name:", config.name);
console.log("Version:", config.version);
console.log("Features:", config.features.join(", "));

// ─────────────────────────────────────────────────────────────
// Module metadata
// ─────────────────────────────────────────────────────────────
console.log("\n--- Module Info ---");
console.log("__filename:", __filename);
console.log("__dirname:", __dirname);
console.log("module.id:", module.id);
console.log("module.loaded:", module.loaded);

// ─────────────────────────────────────────────────────────────
// Module caching
// ─────────────────────────────────────────────────────────────
console.log("\n--- Module Caching ---");
const counter = require('./lib/counter');
console.log("Initial count:", counter.getCount());
counter.increment();
counter.increment();
counter.increment();
console.log("After 3 increments:", counter.getCount());

// Require again - should be cached
const counter2 = require('./lib/counter');
console.log("Same instance?", counter === counter2);
console.log("Count from second require:", counter2.getCount());

// ─────────────────────────────────────────────────────────────
// Nested requires
// ─────────────────────────────────────────────────────────────
console.log("\n--- Nested Requires ---");
const calculator = require('./lib/calculator');
const result = calculator.calculate(10, 5);
console.log("calculate(10, 5):", result);

// ─────────────────────────────────────────────────────────────
// Built-in modules still work
// ─────────────────────────────────────────────────────────────
console.log("\n--- Built-in Modules ---");
const path = require('path');
const crypto = require('crypto');

console.log("path.join(__dirname, 'lib'):", path.join(__dirname, 'lib'));
console.log("Random hex:", crypto.randomBytes(4).toString('hex'));

console.log("\n=== CommonJS Demo Complete ===");
