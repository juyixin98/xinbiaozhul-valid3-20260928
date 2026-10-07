import type { Grammar, InputToken } from "../types.js";
import type { DerivationTree } from "../forest/shared-forest.js";

export interface ReferenceResult {
  status: "complete" | "derivation-budget" | "depth-budget";
  derivations: DerivationTree[];
  expansionsUsed: number;
}

interface Bounds {
  maxDerivations: number;
  maxExpansions: number;
  maxDepth: number;
}

class ReferenceExplorer {
  private expansionsUsed = 0;
  private depthTruncated = false;

  constructor(
    private readonly grammar: Grammar,
    private readonly tokens: readonly InputToken[],
    private readonly bounds: Bounds,
  ) {}

  explore(symbol: string, position: number, depth: number): Array<{ end: number; tree: DerivationTree }> {
    this.expansionsUsed += 1;
    if (
      this.expansionsUsed > this.bounds.maxExpansions ||
      depth > this.bounds.maxDepth
    ) {
      if (depth > this.bounds.maxDepth) {
        this.depthTruncated = true;
      }
      return [];
    }

    if (!this.grammar.nonterminals.has(symbol)) {
      const token = this.tokens[position];
      if (token === undefined || token.kind !== symbol) {
        return [];
      }
      return [
        {
          end: position + 1,
          tree: {
            symbol,
            ruleId: `__terminal_${symbol}`,
            start: position,
            end: position + 1,
            children: [],
            terminal: {
              tokenIndex: position,
              kind: token.kind,
              value: token.value,
            },
          },
        },
      ];
    }

    const results: Array<{ end: number; tree: DerivationTree }> = [];
    const rules = this.grammar.productionsByLhs.get(symbol) ?? [];
    for (const rule of rules) {
      for (const children of this.deriveRhs(rule.rhs, position, 0, depth + 1)) {
        results.push({
          end: children.length === 0 ? position : children[children.length - 1]!.end,
          tree: {
            symbol,
            ruleId: rule.id,
            start: position,
            end: children.length === 0 ? position : children[children.length - 1]!.end,
            children,
          },
        });
      }
    }
    return results;
  }

  private deriveRhs(
    rhs: readonly string[],
    start: number,
    index: number,
    depth: number,
  ): DerivationTree[][] {
    if (index >= rhs.length) {
      return [[]];
    }

    const symbol = rhs[index]!;
    const prefixes = this.explore(symbol, start, depth);
    const combinations: DerivationTree[][] = [];

    for (const prefix of prefixes) {
      const suffixes = this.deriveRhs(rhs, prefix.end, index + 1, depth);
      for (const suffix of suffixes) {
        combinations.push([prefix.tree, ...suffix]);
      }
    }
    return combinations;
  }

  complete(): ReferenceResult {
    const accepted = this.explore(this.grammar.startSymbol, 0, 0)
      .filter((candidate) => candidate.end === this.tokens.length)
      .map((candidate) => candidate.tree);

    return {
      status:
        this.expansionsUsed > this.bounds.maxExpansions || accepted.length >= this.bounds.maxDerivations
          ? "derivation-budget"
          : this.depthTruncated
            ? "depth-budget"
            : "complete",
      derivations: accepted.slice(0, this.bounds.maxDerivations),
      expansionsUsed: this.expansionsUsed,
    };
  }
}

export function enumerateByTopDownExpansion(
  grammar: Grammar,
  tokens: readonly InputToken[],
  bounds: Bounds,
): ReferenceResult {
  return new ReferenceExplorer(grammar, tokens, bounds).complete();
}
