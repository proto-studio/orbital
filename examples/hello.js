// Hello World example for GNode

// Basic expressions work immediately
const greeting = "Hello from GNode!";

// Functions
function fibonacci(n) {
    if (n <= 1) return n;
    return fibonacci(n - 1) + fibonacci(n - 2);
}

// Calculate fib(10)
const result = fibonacci(10);

// Return value will be printed
`${greeting} Fibonacci(10) = ${result}`;
