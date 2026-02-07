// Lodash Example - Demonstrates using npm packages with GNode
// Run with: ./gnode examples/lodash/main.js
//
// First install lodash: cd examples/lodash && npm install lodash

const _ = require('lodash');

console.log('=== Lodash Example ===\n');

// Array operations
console.log('--- Array Operations ---');
const numbers = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];
console.log('Original array:', numbers);
console.log('_.chunk(numbers, 3):', _.chunk(numbers, 3));
console.log('_.compact([0, 1, false, 2, "", 3]):', _.compact([0, 1, false, 2, '', 3]));
console.log('_.drop(numbers, 3):', _.drop(numbers, 3));
console.log('_.take(numbers, 3):', _.take(numbers, 3));
console.log('_.uniq([1, 2, 1, 3, 2, 4]):', _.uniq([1, 2, 1, 3, 2, 4]));
console.log('_.reverse([...numbers]):', _.reverse([...numbers]));

// Collection operations
console.log('\n--- Collection Operations ---');
const users = [
    { name: 'Alice', age: 30, active: true },
    { name: 'Bob', age: 25, active: false },
    { name: 'Charlie', age: 35, active: true },
    { name: 'Diana', age: 28, active: true }
];
console.log('Users:', JSON.stringify(users));
console.log('_.filter(users, {active: true}):', JSON.stringify(_.filter(users, { active: true })));
console.log('_.find(users, {name: "Bob"}):', JSON.stringify(_.find(users, { name: 'Bob' })));
console.log('_.sortBy(users, "age"):', JSON.stringify(_.sortBy(users, 'age')));
console.log('_.map(users, "name"):', _.map(users, 'name'));
console.log('_.groupBy(users, "active"):', JSON.stringify(_.groupBy(users, 'active')));

// Object operations
console.log('\n--- Object Operations ---');
const obj1 = { a: 1, b: 2 };
const obj2 = { b: 3, c: 4 };
console.log('obj1:', JSON.stringify(obj1));
console.log('obj2:', JSON.stringify(obj2));
console.log('_.merge({}, obj1, obj2):', JSON.stringify(_.merge({}, obj1, obj2)));
console.log('_.pick(users[0], ["name", "age"]):', JSON.stringify(_.pick(users[0], ['name', 'age'])));
console.log('_.omit(users[0], ["active"]):', JSON.stringify(_.omit(users[0], ['active'])));
console.log('_.keys(obj1):', _.keys(obj1));
console.log('_.values(obj1):', _.values(obj1));

// String operations
console.log('\n--- String Operations ---');
console.log('_.camelCase("hello world"):', _.camelCase('hello world'));
console.log('_.kebabCase("helloWorld"):', _.kebabCase('helloWorld'));
console.log('_.snakeCase("Hello World"):', _.snakeCase('Hello World'));
console.log('_.capitalize("hello"):', _.capitalize('hello'));
console.log('_.pad("abc", 8):', "'" + _.pad('abc', 8) + "'");
console.log('_.trim("  hello  "):', "'" + _.trim('  hello  ') + "'");
console.log('_.words("hello world foo"):', _.words('hello world foo'));

// Utility operations
console.log('\n--- Utility Operations ---');
console.log('_.range(1, 10):', _.range(1, 10));
console.log('_.random(1, 100):', _.random(1, 100));
console.log('_.times(3, () => "x"):', _.times(3, () => 'x'));
console.log('_.clamp(15, 0, 10):', _.clamp(15, 0, 10));
console.log('_.inRange(5, 1, 10):', _.inRange(5, 1, 10));

// Deep operations
console.log('\n--- Deep Operations ---');
const nested = { a: { b: { c: 3 } } };
console.log('nested:', JSON.stringify(nested));
console.log('_.get(nested, "a.b.c"):', _.get(nested, 'a.b.c'));
console.log('_.get(nested, "a.b.x", "default"):', _.get(nested, 'a.b.x', 'default'));
console.log('_.has(nested, "a.b.c"):', _.has(nested, 'a.b.c'));
console.log('_.has(nested, "a.b.x"):', _.has(nested, 'a.b.x'));

// Clone
const original = { a: 1, b: { c: 2 } };
const cloned = _.cloneDeep(original);
cloned.b.c = 999;
console.log('\n--- Deep Clone ---');
console.log('original:', JSON.stringify(original));
console.log('cloned (modified):', JSON.stringify(cloned));
console.log('Original unchanged:', original.b.c === 2);

// Chaining
console.log('\n--- Method Chaining ---');
const result = _.chain(numbers)
    .filter(n => n % 2 === 0)
    .map(n => n * 2)
    .take(3)
    .value();
console.log('_.chain(numbers).filter(even).map(*2).take(3).value():', result);

console.log('\n=== Lodash Example Complete ===');
