// ES Module: greeting utilities

export function hello(name) {
    return `Hello, ${name}!`;
}

export function goodbye(name) {
    return `Goodbye, ${name}!`;
}

export class Greeter {
    constructor(prefix = "Hello") {
        this.prefix = prefix;
    }

    greet(name) {
        return `${this.prefix}, ${name}!`;
    }
}

// Default export
export default hello;
