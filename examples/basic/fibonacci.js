// Fibonacci example

function fibonacci(n) {
    if (n <= 1) return n;
    return fibonacci(n - 1) + fibonacci(n - 2);
}

console.log("Fibonacci sequence (first 15 numbers):");
for (let i = 0; i < 15; i++) {
    process.stdout && process.stdout.write ? 
        process.stdout.write(fibonacci(i) + " ") : 
        console.log(fibonacci(i));
}
console.log();

// Timing a larger calculation
console.time("fib(35)");
const result = fibonacci(35);
console.timeEnd("fib(35)");
console.log("fib(35) =", result);
