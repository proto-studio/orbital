// CommonJS module
const greeting = "Hello from CommonJS!";

function add(a, b) {
    return a + b;
}

function multiply(a, b) {
    return a * b;
}

// Named exports via module.exports
module.exports = {
    greeting,
    add,
    multiply
};
