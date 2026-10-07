/**
 * Earley deduction kernel with SPPF construction.
 *
 * Why this terminates on empty productions and left recursion
 * ------------------------------------------------------------
 * A chart set is a *set* of items keyed by (rule, dot, origin, parse-node).
 * Predictor expansion and zero-width completion run inside a work queue, and
 * both directions are wired up:
 *
 *   - adding an expecting item registers it against the expected non-terminal;
 *   - adding a zero-width completed item fires every registered expecter;
 *   - adding an expecting item after the fact immediately fires against
 *     zero-width completed items already present.
 *
 * Because item insertion is de-duplicated and the item universe is finite,
 * the closure always reaches a fixed point even for grammars such as
 * `S -> S | epsilon` or `A -> B, B -> A, A -> epsilon`.
 *
 * De-duplication preserves ambiguity: two derivations are collapsed only when
 * they share the *same* SPPF parse node (same rule/dot/span AND same child
 * families); distinct derivations create distinct parse nodes, each carrying
 * its own family.
 */

import type { Grammar, Rule } from "../grammar/types.js";
import { SppfBuilder } from "../forest/sppf.js";
import type { ParseResult } from "./types.js";

export interface EarleyItem {
  ruleIndex: number;
  dot: number;
  origin: number;
  /** SPPF node attached to the matched prefix (intermediate/symbol), or null at dot 0. */
  node: number | null;
}

function itemKey(it: EarleyItem): string {
  return `${it.ruleIndex}:${it.dot}:${it.origin}:${it.node === null ? "_" : it.node}`;
}

interface ChartSet {
  items: EarleyItem[];
  keys: Set<string>;
  /** Non-terminal name -> expecting items waiting for a completion ending here. */
  expecters: Map<string, EarleyItem[]>;
  /** Non-terminal name -> zero-width completed symbol nodes already produced here. */
  zeroCompletions: Map<string, number[]>;
}

function newSet(): ChartSet {
  return { items: [], keys: new Set(), expecters: new Map(), zeroCompletions: new Map() };
}

export interface RunOutput {
  result: ParseResult;
  forest: SppfBuilder;
  chart: ChartSet[];
  grammar: Grammar;
}

export function runEarley(grammar: Grammar, tokens: ReadonlyArray<string>): RunOutput {
  const forest = new SppfBuilder();
  const n = tokens.length;
  const sets: ChartSet[] = [];
  for (let i = 0; i <= n; i++) sets.push(newSet());

  let itemCount = 0;

  /** Build/extend the symbol node wrapping a fully matched RHS intermediate node. */
  const wrapCompleted = (item: EarleyItem, end: number): number => {
    const rule = grammar.rules[item.ruleIndex]!;
    const symNode = forest.getSymbol(rule.lhs, item.origin, end);
    forest.addFamily(symNode, item.node, null, `complete rule${item.ruleIndex}`);
    return symNode;
  };

  /** Completer: advance `expecter` registered in setIdx across a span ending at end. */
  const complete = (
    setIdx: number,
    expecter: EarleyItem,
    end: number,
    childSymbolNode: number,
  ): void => {
    const k = expecter.dot + 1;
    const inter = forest.getIntermediate(expecter.ruleIndex, k, expecter.origin, end);
    forest.addFamily(inter, childSymbolNode, expecter.node, `advance dot to ${k}`);
    addItem(end, { ruleIndex: expecter.ruleIndex, dot: k, origin: expecter.origin, node: inter });
  };

  const addItem = (setIdx: number, item: EarleyItem): void => {
    const set = sets[setIdx]!;
    const key = itemKey(item);
    if (set.keys.has(key)) return;
    set.keys.add(key);
    set.items.push(item);
    itemCount++;

    const rule = grammar.rules[item.ruleIndex]!;
    if (item.dot < rule.rhs.length) {
      const sym = rule.rhs[item.dot]!;
      if (sym.kind === "nonterminal") {
        let bucket = set.expecters.get(sym.name);
        if (!bucket) {
          bucket = [];
          set.expecters.set(sym.name, bucket);
        }
        bucket.push(item);
        // Fire against zero-width completions already present in this same set.
        const prior = set.zeroCompletions.get(sym.name);
        if (prior) for (const node of prior) complete(setIdx, item, setIdx, node);
      }
    } else {
      // Completed item.
      const node = wrapCompleted(item, setIdx);
      if (item.origin === setIdx) {
        // Zero-width completion: fire every expecter already registered here.
        let bucket = set.zeroCompletions.get(rule.lhs);
        if (!bucket) {
          bucket = [];
          set.zeroCompletions.set(rule.lhs, bucket);
        }
        if (!bucket.includes(node)) {
          bucket.push(node);
          const waiters = set.expecters.get(rule.lhs);
          if (waiters) for (const w of [...waiters]) complete(setIdx, w, setIdx, node);
        }
      }
    }
  };

  /** Predictor: seed all productions of non-terminal `name` into set idx. */
  const predict = (idx: number, name: string): void => {
    const prods = grammar.byLhs.get(name);
    if (!prods) return;
    for (const p of prods) addItem(idx, { ruleIndex: p.index, dot: 0, origin: idx, node: null });
  };

  predict(0, grammar.start);

  let rootNodeId: number | null = null;

  for (let i = 0; i <= n; i++) {
    const set = sets[i]!;
    // Work queue drives predictor + zero-width completion to a fixed point.
    let cursor = 0;
    while (cursor < set.items.length) {
      const item = set.items[cursor]!;
      cursor++;
      const rule: Rule = grammar.rules[item.ruleIndex]!;
      if (item.dot < rule.rhs.length) {
        const sym = rule.rhs[item.dot]!;
        if (sym.kind === "nonterminal") {
          predict(i, sym.name);
        } else if (i < n && sym.name === tokens[i]) {
          // Scanner.
          const termNode = forest.getTerminal(sym.name, i, i + 1);
          const k = item.dot + 1;
          const inter = forest.getIntermediate(item.ruleIndex, k, item.origin, i + 1);
          forest.addFamily(inter, termNode, item.node, `scan '${sym.name}'`);
          addItem(i + 1, { ruleIndex: item.ruleIndex, dot: k, origin: item.origin, node: inter });
        }
      } else if (item.origin < i) {
        // Non-zero-width completion: advance expecters in the origin set.
        const child = forest.getSymbol(rule.lhs, item.origin, i);
        const waiters = sets[item.origin]!.expecters.get(rule.lhs);
        if (waiters) for (const w of [...waiters]) complete(i, w, i, child);
      }
      // Zero-width completions are handled inside addItem.
    }

    if (i === n) {
      const candidate = forest.getSymbol(grammar.start, 0, n);
      if (forest.get(candidate).families.length > 0) rootNodeId = candidate;
    }
  }

  const result: ParseResult = rootNodeId
    ? {
        verdict: "accept",
        rootNodeId,
        inputLength: n,
        setCount: n + 1,
        itemCount,
        rejectReason: null,
      }
    : {
        verdict: "reject",
        rootNodeId: null,
        inputLength: n,
        setCount: n + 1,
        itemCount,
        rejectReason: "NO_PARSE",
      };

  return { result, forest, chart: sets, grammar };
}
