interface Point {
  x: number;
  y: number;
}

function dist(a: Point, b: Point): number {
  return Math.sqrt((a.x - b.x) ** 2 + (a.y - b.y) ** 2);
}

enum Dir {
  N,
  E,
  S,
  W,
}

class Vec<T> {
  constructor(public readonly items: readonly T[]) {}
  map<U>(f: (t: T) => U): Vec<U> {
    return new Vec(this.items.map(f));
  }
}

const v = new Vec([1, 2, 3]).map((n) => n * 2);
const total = v.items.reduce((a, b) => a + b, 0);

console.log("TSC_OK", dist({ x: 0, y: 0 }, { x: 3, y: 4 }), Dir.S, total);
