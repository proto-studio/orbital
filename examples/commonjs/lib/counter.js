// Counter module - demonstrates module state and caching
// This module maintains state between requires

let count = 0;

module.exports = {
    increment() {
        count++;
    },
    decrement() {
        count--;
    },
    reset() {
        count = 0;
    },
    getCount() {
        return count;
    }
};
