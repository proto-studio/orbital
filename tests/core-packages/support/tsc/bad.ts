// Intentional type errors: tsc must report TS2322 and exit non-zero.
const n: number = "not a number";

export function f(x: string): number {
  return x;
}
