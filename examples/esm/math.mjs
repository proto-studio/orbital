// ES Module: math utilities

export const PI = 3.14159265359;
export const E = 2.71828182845;

export function add(a, b) {
    return a + b;
}

export function subtract(a, b) {
    return a - b;
}

export function multiply(a, b) {
    return a * b;
}

export function divide(a, b) {
    if (b === 0) {
        throw new Error('Division by zero');
    }
    return a / b;
}

export function square(x) {
    return x * x;
}

export function sqrt(x) {
    return Math.sqrt(x);
}

// Default export
export default {
    PI,
    E,
    add,
    subtract,
    multiply,
    divide,
    square,
    sqrt
};
