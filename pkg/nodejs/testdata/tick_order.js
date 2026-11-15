// Order of process.nextTick callbacks, promise reactions, queueMicrotask and timers. Node
// runs the tick queue, then the microtask queue, until both are empty, whenever the
// outermost callback into JavaScript returns. Each case runs on its own, well after the last.
'use strict';
const { EventEmitter } = require('events');
const { Readable, Writable, PassThrough } = require('stream');
const util = require('util');
const fs = require('fs');

const log = (s) => console.log(s);

const cases = {
  'after the main script'() {
    Promise.resolve().then(() => log('promise'));
    queueMicrotask(() => log('queueMicrotask'));
    process.nextTick(() => log('tick'));
    log('sync');
  },
  'nested'() {
    process.nextTick(() => {
      log('t1');
      Promise.resolve().then(() => { log('p-from-t1'); process.nextTick(() => log('t-from-p-from-t1')); });
      process.nextTick(() => log('t-from-t1'));
    });
    Promise.resolve().then(() => {
      log('p1');
      process.nextTick(() => log('t-from-p1'));
      Promise.resolve().then(() => log('p-from-p1'));
    });
    queueMicrotask(() => { log('qm1'); process.nextTick(() => log('t-from-qm1')); });
    process.nextTick(() => log('t2'));
    Promise.resolve().then(() => log('p2'));
  },
  'ticks queued by promise reactions wait for the microtask queue'() {
    Promise.resolve().then(() => {
      log('p1');
      process.nextTick(() => { log('t-a'); Promise.resolve().then(() => log('p-from-t-a')); });
      process.nextTick(() => log('t-b'));
    });
    Promise.resolve().then(() => log('p2'));
    Promise.resolve().then(() => Promise.resolve().then(() => Promise.resolve().then(() => log('p3 deep'))));
    process.nextTick(() => Promise.resolve().then(() => log('p from tick')));
  },
  'recursive ticks run before any promise'() {
    let n = 0;
    const tick = () => {
      if (++n < 5) {
        log('tick ' + n);
        process.nextTick(tick);
        Promise.resolve().then(() => log('p ' + n));
      }
    };
    tick();
    Promise.resolve().then(() => log('p first'));
  },
  'arguments and this'() {
    process.nextTick(function (a, b) { log('args ' + a + ' ' + b + ' ' + (this === undefined)); }, 1, 2);
  },
  'async functions'() {
    const f = async (name) => {
      log(name + ' start');
      await new Promise((r) => process.nextTick(r));
      log(name + ' after tick');
      await null;
      log(name + ' after null');
    };
    f('a');
    f('b');
    process.nextTick(() => log('main tick'));
    Promise.resolve().then(() => log('main promise'));
  },
  'timers'() {
    for (const name of ['timeout1', 'timeout2']) {
      setTimeout(() => {
        log(name);
        Promise.resolve().then(() => log('promise in ' + name));
        process.nextTick(() => log('tick in ' + name));
      }, 0);
    }
  },
  'interval'() {
    let i = 0;
    const h = setInterval(() => {
      const n = ++i;
      log('interval ' + n);
      Promise.resolve().then(() => log('promise ' + n));
      process.nextTick(() => log('tick ' + n));
      if (n === 3) clearInterval(h);
    }, 1);
  },
  'immediate'() {
    setImmediate(() => {
      log('immediate');
      Promise.resolve().then(() => log('promise in immediate'));
      process.nextTick(() => log('tick in immediate'));
    });
  },
  'I/O callbacks'() {
    fs.readdir('.', () => {
      log('readdir');
      Promise.resolve().then(() => log('promise in readdir'));
      process.nextTick(() => log('tick in readdir'));
      fs.promises.stat('.').then(() => {
        log('promises.stat');
        process.nextTick(() => log('tick in promises.stat'));
        Promise.resolve().then(() => log('promise in promises.stat'));
      });
    });
  },
  'event emitters'() {
    class Ready extends EventEmitter {
      constructor() { super(); process.nextTick(() => this.emit('ready')); }
    }
    new Ready().on('ready', () => log('ready'));
    Promise.resolve().then(() => log('promise'));
    const e = new EventEmitter();
    e.on('x', (from) => { log('x from ' + from); process.nextTick(() => log('tick from x ' + from)); });
    Promise.resolve().then(() => e.emit('x', 'promise'));
    process.nextTick(() => e.emit('x', 'tick'));
    EventEmitter.once(e, 'go').then(() => { log('once resolved'); process.nextTick(() => log('tick after once')); });
    process.nextTick(() => e.emit('go'));
  },
  'streams'() {
    const r = new Readable({ read() {} });
    r.push('a');
    r.push('b');
    r.push(null);
    r.on('data', (d) => log('data ' + d));
    r.on('end', () => log('end'));
    Promise.resolve().then(() => log('promise'));
    process.nextTick(() => log('tick'));
    const p = new PassThrough();
    p.on('data', (d) => log('pt ' + d));
    p.write('x', () => log('pt write cb'));
    p.end(() => log('pt end cb'));
    p.on('finish', () => log('pt finish'));
    const w = new Writable({ write(c, e, cb) { log('w ' + c); process.nextTick(cb); } });
    w.write('1', () => log('w1 cb'));
    w.write('2', () => log('w2 cb'));
    w.end(() => log('w end'));
  },
  'util'() {
    util.callbackify(async () => 'v')((err, v) => log('callbackify ' + v));
    util.promisify((cb) => process.nextTick(cb, null, 'pv'))().then((v) => log('promisify ' + v));
    Promise.resolve().then(() => log('promise'));
    process.nextTick(() => log('tick'));
  },
  'many ticks'() {
    let count = 0;
    Promise.resolve().then(() => log('promise after ' + count + ' ticks'));
    const t = () => { if (++count < 1000) process.nextTick(t); };
    process.nextTick(t);
  },
  'exceptions'() {
    process.on('uncaughtException', function handler(e) {
      log('uncaught ' + e.message);
      process.nextTick(() => log('tick from handler'));
    });
    setTimeout(() => {
      Promise.resolve().then(() => log('p before'));
      process.nextTick(() => { throw new Error('in tick'); });
      process.nextTick(() => log('t after'));
      queueMicrotask(() => { throw new Error('in queueMicrotask'); });
      queueMicrotask(() => log('after queueMicrotask'));
    }, 1);
  },
  'unhandled rejections'() {
    process.on('unhandledRejection', (r) => {
      log('unhandled ' + r);
      process.nextTick(() => log('tick in unhandled'));
    });
    Promise.reject('a');
    process.nextTick(() => log('tick'));
    Promise.resolve().then(() => log('promise'));
    const q = Promise.reject('b');
    process.nextTick(() => q.catch(() => log('caught in tick')));
  },
  'validation'() {
    for (const value of [1, undefined, {}, 'abc']) {
      try { process.nextTick(value); } catch (e) { log(e.name + ' ' + e.code + ' ' + e.message); }
    }
  },
};

const names = Object.keys(cases);
const run = (i) => {
  if (i === names.length) return;
  log('== ' + names[i]);
  cases[names[i]]();
  setTimeout(() => run(i + 1), 40);
};
run(0);
