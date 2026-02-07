// Console API examples

console.log("=== Console API Demo ===\n");

// Basic logging
console.log("console.log - standard output");
console.info("console.info - informational");
console.warn("console.warn - warning message");
console.error("console.error - error message");
console.debug("console.debug - debug message");

// String formatting
console.log("\n--- Formatting ---");
console.log("String: %s", "hello");
console.log("Number: %d", 42);
console.log("Float: %f", 3.14159);
console.log("JSON: %j", { key: "value" });
console.log("Multiple: %s has %d items", "Array", 5);

// Assertions
console.log("\n--- Assertions ---");
console.assert(true, "This won't show");
console.assert(1 === 1, "Math works");
console.assert(false, "This assertion failed!");

// Counting
console.log("\n--- Counting ---");
console.count("loop");
console.count("loop");
console.count("loop");
console.count("other");
console.countReset("loop");
console.count("loop"); // Starts over at 1

// Timing
console.log("\n--- Timing ---");
console.time("operation");
let sum = 0;
for (let i = 0; i < 1000000; i++) {
    sum += i;
}
console.timeLog("operation", "halfway point, sum =", sum);
for (let i = 0; i < 1000000; i++) {
    sum += i;
}
console.timeEnd("operation");

// Grouping
console.log("\n--- Grouping ---");
console.group("Outer Group");
console.log("Item 1");
console.log("Item 2");
console.group("Inner Group");
console.log("Nested item A");
console.log("Nested item B");
console.groupEnd();
console.log("Item 3");
console.groupEnd();
console.log("Outside groups");

// Table
console.log("\n--- Table ---");
console.table(['apple', 'banana', 'cherry']);
console.table([10, 20, 30, 40, 50]);

// Trace
console.log("\n--- Trace ---");
function outer() {
    function inner() {
        console.trace("Stack trace");
    }
    inner();
}
outer();

console.log("\n=== Console Demo Complete ===");
