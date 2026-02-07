// Calculator module - demonstrates nested requires

// This module requires another local module
const math = require('./math');

module.exports = {
    calculate(a, b) {
        return {
            sum: math.add(a, b),
            difference: math.subtract(a, b),
            product: math.multiply(a, b),
            quotient: math.divide(a, b)
        };
    },
    
    // Re-export math for convenience
    math: math
};
