/**
 * Forest enumeration, deliberately separate from recognition.
 *
 * Recognition only checks that a root symbol node exists with at least one
 * family. Counting / tree materialization walks the SPPF and therefore needs a
 * hard budget: cyclic nullable grammars (e.g. `S -> S | epsilon`) are accepted
 * but admit infinitely many derivations, and even finite forests can be large.
 *
 * Counting is exact (bigint) on finite forests. A reachable dependency cycle
 * over zero-width nodes means the count is infinite; we report that as
 * `infinite` rather than guessing.
 */

import type { SppfBuilder, SppfNode } from "./sppf.js";

export interface EnumerationBudget {
  /** Maximum node-family steps before the walk stops. */
  readonly maxSteps: number;
  /** Maximum concrete parse trees to materialize (0 disables trees). */
  readonly maxTrees?: number;
}

export type EnumerationStatus =
  | "exact"
  | "infinite" // cyclic zero-width derivation: infinitely many parses
  | "budget_exhausted"; // walked past maxSteps without finishing

export interface CountResult {
  status: EnumerationStatus;
  /** Exact count when status === "exact", otherwise a lower bound. */
  count: bigint;
  stepsUsed: number;
}

export interface ParseTree {
  label: string;
  span: [number, number];
  children: ParseTree[];
}

interface CounterState {
  memo: Map<number, bigint>;
  color: Map<number, 0 | 1 | 2>; // 0 white, 1 gray (on stack), 2 black
  steps: number;
  exhausted: boolean;
  infiniteNode: boolean;
}

/**
 * Count the number of distinct parse trees rooted at `rootId`.
 *
 * For each node the count is the sum over families of the product of child
 * counts. Tarjan-style coloring detects a zero-width recursive cycle: reaching
 * a gray node implies an unbounded number of pumpings, i.e. infinitely many
 * derivations.
 */
export function countParses(
  forest: SppfBuilder,
  rootId: number,
  budget: EnumerationBudget,
): CountResult {
  const state: CounterState = {
    memo: new Map(),
    color: new Map(),
    steps: 0,
    exhausted: false,
    infiniteNode: false,
  };

  const visit = (id: number): bigint => {
    if (state.infiniteNode || state.exhausted) return 0n;
    const color = state.color.get(id) ?? 0;
    if (color === 2) return state.memo.get(id) ?? 0n;
    if (color === 1) {
      // Recursive zero-width cycle (all nodes along a zero-width edge share a
      // span): infinitely many derivations.
      state.infiniteNode = true;
      return 0n;
    }
    state.color.set(id, 1);
    const node = forest.get(id);
    if (node.kind === "terminal" || node.kind === "empty") {
      state.color.set(id, 2);
      state.memo.set(id, 1n);
      return 1n;
    }
    let total = 0n;
    for (const fam of node.families) {
      let ways = 1n;
      if (fam.left !== null) ways *= visit(fam.left);
      if (fam.right !== null) ways *= visit(fam.right);
      total += ways;
      state.steps++;
      if (state.steps > budget.maxSteps) {
        state.exhausted = true;
        break;
      }
    }
    state.color.set(id, 2);
    state.memo.set(id, total);
    return total;
  };

  const count = visit(rootId);
  if (state.infiniteNode) {
    return { status: "infinite", count: acyclicLowerBound(forest, rootId, budget), stepsUsed: state.steps };
  }
  if (state.exhausted) {
    return { status: "budget_exhausted", count, stepsUsed: state.steps };
  }
  return { status: "exact", count, stepsUsed: state.steps };
}

/** Conservative lower bound: count families reachable without revisiting a node. */
function acyclicLowerBound(forest: SppfBuilder, rootId: number, budget: EnumerationBudget): bigint {
  const seen = new Set<number>();
  let steps = 0;
  const walk = (id: number): bigint => {
    if (seen.has(id)) return 0n;
    seen.add(id);
    const node: SppfNode = forest.get(id);
    if (node.kind === "terminal" || node.kind === "empty") return 1n;
    let total = 0n;
    for (const fam of node.families) {
      let ways = 1n;
      if (fam.left !== null) ways *= walk(fam.left);
      if (fam.right !== null) ways *= walk(fam.right);
      total += ways;
      steps++;
      if (steps > budget.maxSteps) break;
    }
    return total;
  };
  return walk(rootId);
}

/**
 * Materialize concrete parse trees up to `limit`. Family/child order is the
 * forest insertion order, so output is stable.
 */
export function enumerateTrees(
  forest: SppfBuilder,
  rootId: number,
  limit: number,
): { trees: ParseTree[]; truncated: boolean } {
  const trees: ParseTree[] = [];
  let truncated = false;

  const isIntermediateLabel = (label: string): boolean => label.startsWith("rule");

  const build = (id: number): ParseTree[] => {
    const node = forest.get(id);
    if (node.kind === "terminal" || node.kind === "empty") {
      return [{ label: node.label, span: [node.start, node.end], children: [] }];
    }
    const out: ParseTree[] = [];
    for (const fam of node.families) {
      const lefts = fam.left === null ? [null] : build(fam.left);
      const rights = fam.right === null ? [null] : build(fam.right);
      for (const l of lefts) {
        for (const r of rights) {
          const children: ParseTree[] = [];
          // Right child chains prefixes in reverse; unwrap intermediate nodes so
          // the rendered tree reads in RHS order.
          const prefix: ParseTree[] = [];
          if (r) collectRhs(r, prefix, isIntermediateLabel);
          children.push(...prefix.reverse());
          if (l) children.push(...present(l, isIntermediateLabel));
          out.push({ label: node.label, span: [node.start, node.end], children });
          if (out.length >= limit) {
            truncated = true;
            return out.slice(0, limit);
          }
        }
      }
    }
    return out;
  };

  trees.push(...build(rootId).slice(0, limit));
  return { trees, truncated };
}

function collectRhs(
  tree: ParseTree,
  acc: ParseTree[],
  isIntermediate: (label: string) => boolean,
): void {
  if (isIntermediate(tree.label)) {
    // Intermediate children are stored [latestSymbol, ...prefix] (see build).
    for (const child of tree.children) acc.push(child);
  } else {
    acc.push(tree);
  }
}

function present(
  tree: ParseTree,
  isIntermediate: (label: string) => boolean,
): ParseTree[] {
  return isIntermediate(tree.label) ? tree.children : [tree];
}
