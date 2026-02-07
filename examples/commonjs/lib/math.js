// Math utility module

// Export individual functions using exports shorthand
exports.add = (a, b) => a + b;
exports.subtract = (a, b) => a - b;
exports.multiply = (a, b) => a * b;
exports.divide = (a, b) => {
    if (b === 0) throw new Error("Division by zero");
    return a / b;
};

// Export constants
exports.PI = 3.14159265359;
exports.E = 2.71828182846;

// Export more complex functions
exports.circleArea = (radius) => exports.PI * radius * radius;
exports.factorial = (n) => {
    if (n <= 1) return 1;
    return n * exports.factorial(n - 1);
};
