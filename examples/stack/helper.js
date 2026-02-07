// helper.js - Imported module with nested function calls

function helperLevel1(value) {
    console.log('helper.js: helperLevel1 called with', value);
    return helperLevel2(value * 2);
}

function helperLevel2(value) {
    console.log('helper.js: helperLevel2 called with', value);
    return deepThrow(value);
}

function deepThrow(value) {
    console.log('helper.js: deepThrow called with', value);
    
    // This will throw an error deep in the call stack
    throw new Error(`Something went wrong with value: ${value}`);
}

module.exports = {
    helperLevel1,
    helperLevel2,
    deepThrow
};
