// ES Module: Re-export example

// Re-export everything from math
export * from './math.mjs';

// Re-export specific items from greet
export { hello, goodbye } from './greet.mjs';

// Re-export with renaming
export { Greeter as WelcomeGreeter } from './greet.mjs';

// Add our own utilities
export function formatNumber(num, decimals = 2) {
    return num.toFixed(decimals);
}

export function repeat(str, times) {
    return str.repeat(times);
}
