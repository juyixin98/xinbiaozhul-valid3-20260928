/**
 * Independent reference derivation counter.
 *
 * Shares nothing with the Earley kernel or the SPPF. It uses interval dynamic
 * programming over input positions:
 *
 *   ways[nt][i][j] = number of derivations nt =>* tokens[i:j]
 *
 * For a rule A -> X1..Xk it enumerates every split of span [i,j] among the k
 * symbols and multiplies per-symbol counts. Positive-width spans are computed
 * by increasing length. Zero-width behavior is decided structurally (a
 * reachable cycle in the nullable dependency graph => infinitely many
 * derivations), and finite zero-width counts use an acyclic memoized DFS.
 */

import type { Grammar, GSymbol } from "../grammar/types.js";

export type RefStatus = "exact" | "infinite";

export interface RefResult {
  status: RefStatus;
  /** Exact count, or the positive-width count when infinite. */
  count: bigint;
}

type SpanTable = Map<number, Map<number, bigint>>;

const makeTable = (): SpanTable => new Map();

function get(table: SpanTable, i: number, j: number): bigint {
  return table.get(i)?.get(j) ?? 0n;
}

function add(table: SpanTable, i: number, j: number, v: bigint): void {
  let row = table.get(i);
  if (!row) {
    row = new Map();
    table.set(i, row);
  }
  row.set(j, (row.get(j) ?? 0n) + v);
}

export function referenceCount(grammar: Grammar, tokens: ReadonlyArray<string>): RefResult {
  const n = tokens.length;
  const nt = new Map<string, SpanTable>();
  for (const name of grammar.nonterminals) nt.set(name, makeTable());

  const symbolWays = (sym: GSymbol, i: number, j: number): bigint => {
    if (sym.kind === "terminal") return i + 1 === j && tokens[i] === sym.name ? 1n : 0n;
    return get(nt.get(sym.name)!, i, j);
  };

  const seqWays = (
    rhs: ReadonlyArray<GSymbol>,
    startSym: number,
    pos: number,
    end: number,
  ): bigint => {
    if (startSym === rhs.length) return pos === end ? 1n : 0n;
    let total = 0n;
    for (let k = pos; k <= end; k++) {
      const head = symbolWays(rhs[startSym]!, pos, k);
      if (head === 0n) continue;
      total += head * seqWays(rhs, startSym + 1, k, end);
    }
    return total;
  };

  // Positive-width spans by increasing length.
  for (let len = 1; len <= n; len++) {
    for (let i = 0; i + len <= n; i++) {
      const j = i + len;
      for (const rule of grammar.rules) {
        add(nt.get(rule.lhs)!, i, j, seqWays(rule.rhs, 0, i, j));
      }
    }
  }

  // Structural pump detection: edges A -> X for each non-terminal X in a
  // fully nullable RHS. A reachable cycle means unbounded zero-width pumping.
  const deps = new Map<string, Set<string>>();
  for (const rule of grammar.rules) {
    const allNullable = rule.rhs.every(
      (s) => s.kind === "nonterminal" && grammar.nullable.has(s.name),
    );
    if (!allNullable) continue;
    let set = deps.get(rule.lhs);
    if (!set) {
      set = new Set();
      deps.set(rule.lhs, set);
    }
    for (const s of rule.rhs) if (s.kind === "nonterminal") set.add(s.name);
  }
  const infinite = grammar.nullable.has(grammar.start) && reachableCycle(grammar.start, deps);

  // Finite zero-width counts via acyclic memoized DFS.
  const zeroMemo = new Map<string, bigint>();
  const zeroColor = new Map<string, 0 | 1 | 2>();
  const zeroWays = (name: string): bigint => {
    if (infinite) return 0n;
    if ((zeroColor.get(name) ?? 0) === 2) return zeroMemo.get(name)!;
    zeroColor.set(name, 1);
    let total = 0n;
    for (const rule of grammar.byLhs.get(name) ?? []) {
      let ways = 1n;
      for (const s of rule.rhs) {
        if (s.kind === "terminal" || (zeroColor.get(s.name) ?? 0) === 1) {
          ways = 0n;
          break;
        }
        ways *= zeroWays(s.name);
      }
      total += ways; // empty RHS keeps ways === 1
    }
    zeroColor.set(name, 2);
    zeroMemo.set(name, total);
    return total;
  };

  const finalPositive = get(nt.get(grammar.start)!, 0, n);
  const finalZero = n === 0 ? zeroWays(grammar.start) : 0n;

  if (infinite && (n === 0 || finalPositive > 0n)) {
    return { status: "infinite", count: finalPositive };
  }
  return { status: "exact", count: finalPositive + finalZero };
}

function reachableCycle(start: string, deps: Map<string, Set<string>>): boolean {
  const color = new Map<string, 0 | 1 | 2>();
  let found = false;
  const dfs = (name: string): void => {
    if (found) return;
    const c = color.get(name) ?? 0;
    if (c === 1) {
      found = true;
      return;
    }
    if (c === 2) return;
    color.set(name, 1);
    for (const next of deps.get(name) ?? []) dfs(next);
    color.set(name, 2);
  };
  dfs(start);
  return found;
}

/** Every token string over `alphabet` up to `maxLength`, shortest first. */
export function enumerateShortStrings(
  alphabet: ReadonlyArray<string>,
  maxLength: number,
): string[][] {
  const out: string[][] = [[]];
  let frontier: string[][] = [[]];
  for (let len = 1; len <= maxLength; len++) {
    const next: string[][] = [];
    for (const pref of frontier) {
      for (const t of alphabet) next.push([...pref, t]);
    }
    out.push(...next);
    frontier = next;
  }
  return out;
}
