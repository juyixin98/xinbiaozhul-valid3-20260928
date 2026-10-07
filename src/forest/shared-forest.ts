import type { InputToken } from "../types.js";
import type { EarleyItem, PackedLink, RecognitionResult } from "../kernel/earley.js";

export interface DerivationTree {
  symbol: string;
  ruleId: string;
  start: number;
  end: number;
  children: DerivationTree[];
  terminal?: {
    tokenIndex: number;
    kind: string;
    value: string;
  };
}

export type EnumerationStatus =
  | "complete"
  | "derivation-budget"
  | "expansion-budget";

export interface ForestEnumeration {
  status: EnumerationStatus;
  derivationCount: number;
  derivations: DerivationTree[];
  expansionsUsed: number;
  budgets: {
    maxDerivations: number;
    maxExpansions: number;
  };
}

export interface ForestBudgets {
  maxDerivations: number;
  maxExpansions: number;
}

class Enumerator {
  private expansionsUsed = 0;
  private derivationCount = 0;
  private truncated = false;
  private readonly itemsById = new Map<number, EarleyItem>();

  constructor(
    private readonly result: RecognitionResult,
    private readonly budgets: ForestBudgets,
  ) {
    for (const chart of result.charts) {
      for (const item of chart.items) {
        this.itemsById.set(item.id, item);
      }
    }
  }

  enumerate(): ForestEnumeration {
    const derivations: DerivationTree[] = [];

    if (this.result.accepted) {
      for (const root of this.result.startItems) {
        outer: for (const tree of this.treesForItem(root)) {
          if (this.truncated) {
            break outer;
          }
          derivations.push(tree);
          this.derivationCount += 1;
          if (this.derivationCount >= this.budgets.maxDerivations) {
            this.truncated = true;
          }
        }
      }
    }

    const status: EnumerationStatus = this.expansionsUsed >= this.budgets.maxExpansions
      ? "expansion-budget"
      : this.truncated
        ? "derivation-budget"
        : "complete";

    return {
      status,
      derivationCount: derivations.length,
      derivations,
      expansionsUsed: this.expansionsUsed,
      budgets: this.budgets,
    };
  }

  private *treesForItem(item: EarleyItem): Generator<DerivationTree> {
    this.expansionsUsed += 1;
    if (this.expansionsUsed > this.budgets.maxExpansions) {
      this.truncated = true;
      return;
    }

    for (const link of item.links) {
      yield* this.treesForLink(item, link);
      if (this.truncated) {
        return;
      }
    }
  }

  private *treesForLink(item: EarleyItem, link: PackedLink): Generator<DerivationTree> {
    if (link.kind === "epsilon") {
      yield {
        symbol: item.rule.lhs,
        ruleId: item.rule.id,
        start: item.start,
        end: item.end,
        children: [],
      };
      return;
    }

    if (link.kind === "scan") {
      const waiter = this.itemsById.get(link.waiterItemId);
      if (waiter === undefined) {
        throw new Error(`Missing waiter item ${link.waiterItemId}`);
      }
      const token = this.result.tokens[link.tokenIndex];
      if (token === undefined) {
        throw new Error(`Missing token ${link.tokenIndex}`);
      }

      yield* this.combinePrefix(item, waiter, [this.terminalTree(token, link.tokenIndex)]);
      return;
    }

    const waiter = this.itemsById.get(link.waiterItemId);
    const completed = this.itemsById.get(link.completedItemId);
    if (waiter === undefined || completed === undefined) {
      throw new Error("Packed link references a missing Earley item");
    }

    for (const completedTree of this.treesForItem(completed)) {
      yield* this.combinePrefix(item, waiter, [completedTree]);
      if (this.truncated) {
        return;
      }
    }
  }

  private *combinePrefix(
    item: EarleyItem,
    waiter: EarleyItem,
    suffixChildren: DerivationTree[],
  ): Generator<DerivationTree> {
    if (waiter.dot === 0) {
      yield {
        symbol: item.rule.lhs,
        ruleId: item.rule.id,
        start: item.start,
        end: item.end,
        children: suffixChildren,
      };
      return;
    }

    for (const waiterTree of this.treesForItem(waiter)) {
      yield {
        symbol: item.rule.lhs,
        ruleId: item.rule.id,
        start: item.start,
        end: item.end,
        children: [...waiterTree.children, ...suffixChildren],
      };
      if (this.truncated) {
        return;
      }
    }
  }

  private terminalTree(token: InputToken, tokenIndex: number): DerivationTree {
    return {
      symbol: token.kind,
      ruleId: `__terminal_${token.kind}`,
      start: tokenIndex,
      end: tokenIndex + 1,
      children: [],
      terminal: {
        tokenIndex,
        kind: token.kind,
        value: token.value,
      },
    };
  }
}

export function enumerateForest(
  result: RecognitionResult,
  budgets: ForestBudgets,
): ForestEnumeration {
  return new Enumerator(result, budgets).enumerate();
}
