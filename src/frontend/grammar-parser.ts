import type { Grammar, ProductionRule } from "../types.js";
import { GrammarSyntaxError } from "../types.js";

interface RawRule {
  lhs: string;
  rhs: string[];
  line: number;
}

const IDENTIFIER = /^[A-Za-z_][A-Za-z0-9_-]*$/u;

function unquoteTerminal(symbol: string, line: number, column: number): string {
  if (symbol.length < 3 || !symbol.startsWith("'") || !symbol.endsWith("'")) {
    throw new GrammarSyntaxError("Malformed quoted terminal", line, column);
  }
  const value = symbol.slice(1, -1);
  if (value.length === 0) {
    throw new GrammarSyntaxError("Empty quoted terminal is not allowed; use '.' for epsilon", line, column);
  }
  return value;
}

export function parseGrammar(source: string, startSymbol = "S"): Grammar {
  const rawRules: RawRule[] = [];
  const lines = source.split(/\r\n|\n|\r/u);

  lines.forEach((lineText, zeroBasedLine) => {
    const line = zeroBasedLine + 1;
    const withoutComment = lineText.replace(/#.*$/u, "").trim();
    if (withoutComment.length === 0) {
      return;
    }

    const parts = withoutComment.split(/\s+/u);
    const arrowIndex = parts.indexOf("->");
    if (arrowIndex <= 0 || parts[0] === undefined) {
      throw new GrammarSyntaxError("Expected '<nonterminal> -> <rhs>'", line);
    }

    const lhs = parts[0];
    if (!IDENTIFIER.test(lhs)) {
      throw new GrammarSyntaxError(`Invalid nonterminal '${lhs}'`, line, 1);
    }

    let rhs: string[] = [];
    let hasAlternative = false;
    for (let index = arrowIndex + 1; index < parts.length; index += 1) {
      const symbol = parts[index];
      if (symbol === undefined) {
        continue;
      }
      if (symbol === "|") {
        rawRules.push({ lhs, rhs, line });
        rhs = [];
        hasAlternative = true;
        continue;
      }
      hasAlternative = false;
      if (symbol === ".") {
        if (rhs.length !== 0) {
          throw new GrammarSyntaxError("Epsilon '.' must be the only right-hand-side symbol", line);
        }
        rawRules.push({ lhs, rhs: [], line });
        rhs = [];
        hasAlternative = true;
        continue;
      }
      if (symbol.startsWith("'")) {
        rhs.push(unquoteTerminal(symbol, line, withoutComment.indexOf(symbol) + 1));
      } else {
        rhs.push(symbol);
      }
    }
    if (!hasAlternative) {
      rawRules.push({ lhs, rhs, line });
    }
  });

  if (rawRules.length === 0) {
    throw new GrammarSyntaxError("Grammar contains no productions");
  }

  const nonterminals = new Set(rawRules.map((rule) => rule.lhs));
  if (!nonterminals.has(startSymbol)) {
    throw new GrammarSyntaxError(`Start symbol '${startSymbol}' has no productions`);
  }

  for (const rule of rawRules) {
    for (const symbol of rule.rhs) {
      if (nonterminals.has(symbol)) {
        continue;
      }
      if (IDENTIFIER.test(symbol)) {
        throw new GrammarSyntaxError(
          `Symbol '${symbol}' is used but has no productions at line ${rule.line}`,
          rule.line,
        );
      }
    }
  }

  const rules: ProductionRule[] = rawRules.map((rule, index) => ({
    id: `r${index}_${rule.lhs}`,
    lhs: rule.lhs,
    rhs: rule.rhs,
  }));

  const terminals = new Set<string>();
  const productionsByLhs = new Map<string, ProductionRule[]>();
  for (const rule of rules) {
    const bucket = productionsByLhs.get(rule.lhs) ?? [];
    bucket.push(rule);
    productionsByLhs.set(rule.lhs, bucket);
    for (const symbol of rule.rhs) {
      if (!nonterminals.has(symbol)) {
        terminals.add(symbol);
      }
    }
  }

  return { startSymbol, rules, nonterminals, terminals, productionsByLhs };
}
