import { EPSILON, GrammarError, type Grammar, type GSymbol, type Rule } from "./types.js";

interface RawAlternative {
  symbols: GSymbol[];
  line: number;
}

interface RawRule {
  lhs: string;
  alts: RawAlternative[];
  line: number;
}

interface Token {
  kind: "ident" | "terminal" | "arrow" | "pipe" | "semi";
  text: string;
  line: number;
  column: number;
}

function lex(source: string): { tokens: Token[]; issues: import("./types.js").GrammarIssue[] } {
  const tokens: Token[] = [];
  const issues: import("./types.js").GrammarIssue[] = [];
  const lines = source.split(/\r?\n/);
  for (let li = 0; li < lines.length; li++) {
    const line = lines[li]!;
    let i = 0;
    const pushIssue = (column: number, message: string) =>
      issues.push({ severity: "error", code: "GRAMMAR_SYNTAX", message, line: li + 1, column });
    while (i < line.length) {
      const ch = line[i]!;
      if (ch === "#") break; // line comment
      if (/\s/.test(ch)) {
        i++;
        continue;
      }
      const col = i + 1;
      if (ch === "'" || ch === '"') {
        const quote = ch;
        let j = i + 1;
        let value = "";
        let closed = false;
        while (j < line.length) {
          const c = line[j]!;
          if (c === "\\" && j + 1 < line.length) {
            value += line[j + 1];
            j += 2;
            continue;
          }
          if (c === quote) {
            closed = true;
            j++;
            break;
          }
          value += c;
          j++;
        }
        if (!closed) {
          pushIssue(col, "unterminated string literal");
          i = line.length;
          continue;
        }
        tokens.push({ kind: "terminal", text: value, line: li + 1, column: col });
        i = j;
        continue;
      }
      if (ch === "-" && line[i + 1] === ">") {
        tokens.push({ kind: "arrow", text: "->", line: li + 1, column: col });
        i += 2;
        continue;
      }
      if (ch === "|") {
        tokens.push({ kind: "pipe", text: "|", line: li + 1, column: col });
        i++;
        continue;
      }
      if (ch === ";") {
        tokens.push({ kind: "semi", text: ";", line: li + 1, column: col });
        i++;
        continue;
      }
      if (/[A-Za-z_0-9]/.test(ch)) {
        let j = i;
        while (j < line.length && /[A-Za-z_0-9]/.test(line[j]!)) j++;
        tokens.push({ kind: "ident", text: line.slice(i, j), line: li + 1, column: col });
        i = j;
        continue;
      }
      pushIssue(col, `unexpected character ${JSON.stringify(ch)}`);
      i++;
    }
  }
  return { tokens, issues };
}

export function parseGrammar(source: string, startSymbol = "S"): Grammar {
  const { tokens, issues } = lex(source);
  const rawRules: RawRule[] = [];
  let p = 0;

  const issue = (
    token: Token | undefined,
    code: import("./types.js").GrammarIssue["code"],
    message: string,
  ): never => {
    issues.push({
      severity: "error",
      code,
      message,
      line: token?.line ?? 1,
      column: token?.column ?? 1,
    });
    throw new GrammarError(issues);
  };

  while (p < tokens.length) {
    const lhsTok = tokens[p];
    if (!lhsTok || lhsTok.kind !== "ident") issue(lhsTok, "GRAMMAR_SYNTAX", "expected non-terminal name");
    p++;
    const arrow = tokens[p];
    if (!arrow || arrow.kind !== "arrow") issue(arrow, "GRAMMAR_SYNTAX", "expected '->' after non-terminal");
    p++;
    const alts: RawAlternative[] = [];
    let symbols: GSymbol[] = [];
    let sawAlt = false;
    const ruleStartLine = lhsTok!.line;
    const finishAlt = () => {
      alts.push({ symbols, line: ruleStartLine });
      symbols = [];
    };
    while (p < tokens.length) {
      const t = tokens[p]!;
      if (t.kind === "semi") {
        p++;
        break;
      }
      if (t.kind === "pipe") {
        finishAlt();
        sawAlt = true;
        p++;
        continue;
      }
      if (t.kind === "ident") {
        // An ident followed by '->' on a fresh RHS position terminates the rule
        // even without a semicolon (newline-insensitive, so only at boundary).
        symbols.push({ kind: "nonterminal", name: t.text });
        sawAlt = true;
        p++;
        continue;
      }
      if (t.kind === "terminal") {
        symbols.push({ kind: "terminal", name: t.text });
        sawAlt = true;
        p++;
        continue;
      }
      issue(t, "GRAMMAR_SYNTAX", "expected a symbol, '|', or ';'");
    }
    if (!sawAlt && alts.length === 0) {
      // `A -> ;` or `A ->` means A -> epsilon.
      symbols = [];
    }
    finishAlt();
    rawRules.push({ lhs: lhsTok!.text, alts, line: ruleStartLine });
  }

  if (issues.length > 0) throw new GrammarError(issues);
  if (rawRules.length === 0) {
    throw new GrammarError([
      { severity: "error", code: "GRAMMAR_NO_RULES", message: "grammar contains no rules", line: 1, column: 1 },
    ]);
  }

  // Expand alternatives into individual rules while preserving source order.
  const rules: Rule[] = [];
  const byLhs = new Map<string, Rule[]>();
  const nonterminals = new Set<string>();
  const terminals = new Set<string>();
  for (const raw of rawRules) {
    nonterminals.add(raw.lhs);
    for (const alt of raw.alts) {
      const rhs = alt.symbols
        .map(stripEpsilon)
        .filter((s): s is GSymbol => s !== null);
      const rule: Rule = { index: rules.length, lhs: raw.lhs, rhs, line: alt.line };
      rules.push(rule);
      let bucket = byLhs.get(raw.lhs);
      if (!bucket) {
        bucket = [];
        byLhs.set(raw.lhs, bucket);
      }
      bucket.push(rule);
      for (const sym of rule.rhs) {
        if (sym.kind === "terminal") terminals.add(sym.name);
        else nonterminals.add(sym.name);
      }
    }
  }

  // Defined non-terminals are exactly those appearing on some LHS.
  for (const nt of nonterminals) {
    if (!byLhs.has(nt)) {
      throw new GrammarError([
        {
          severity: "error",
          code: "GRAMMAR_UNDEFINED_NONTERMINAL",
          message: `non-terminal '${nt}' is used but has no productions`,
          line: 1,
          column: 1,
        },
      ]);
    }
  }
  if (!byLhs.has(startSymbol)) {
    throw new GrammarError([
      {
        severity: "error",
        code: "GRAMMAR_START_WITHOUT_RULE",
        message: `start symbol '${startSymbol}' has no productions`,
        line: 1,
        column: 1,
      },
    ]);
  }

  const nullable = computeNullable(rules);
  return { start: startSymbol, rules, byLhs, nonterminals, terminals, nullable };
}

function stripEpsilon(sym: GSymbol): GSymbol | null {
  // The reserved word `epsilon` marks an empty position and is dropped.
  if (sym.kind === "nonterminal" && sym.name === EPSILON) return null;
  return sym;
}

/**
 * Fixed-point of nullability. Termination is guaranteed: each pass only ever
 * adds non-terminals to the set, and the domain is finite.
 */
export function computeNullable(rules: ReadonlyArray<Rule>): Set<string> {
  const nullable = new Set<string>();
  let changed = true;
  let guard = 0;
  const bound = rules.length + 1;
  while (changed) {
    changed = false;
    for (const r of rules) {
      if (nullable.has(r.lhs)) continue;
      if (r.rhs.every((s) => (s.kind === "nonterminal" ? nullable.has(s.name) : false))) {
        nullable.add(r.lhs);
        changed = true;
      }
    }
    guard++;
    if (guard > bound) {
      // Defensive: cannot happen for finite grammars, but keeps the closure honest.
      throw new Error("nullable fixed-point failed to terminate");
    }
  }
  return nullable;
}
