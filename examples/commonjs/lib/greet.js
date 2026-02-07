// Greeting module - demonstrates module.exports

function hello(name) {
    return `Hello, ${name}!`;
}

function goodbye(name) {
    return `Goodbye, ${name}!`;
}

function formal(title, lastName) {
    return `Good day, ${title} ${lastName}.`;
}

// Export all functions as an object
module.exports = {
    hello,
    goodbye,
    formal
};
