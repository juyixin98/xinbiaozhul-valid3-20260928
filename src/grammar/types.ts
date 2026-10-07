/**
 * Language front-end: grammar and token model.
 *
 * A grammar is a context-free grammar (CFG) in a small BNF dialect:
 *
 *   Rule     ::= NonTerminal "->" Symbol* ("|" Symbol*)*
 *   Symbol   ::= NonTerminal | Terminal | "epsilon"
 *   Terminal ::= a single-quoted string, e.g. '+' 'id' '('
 *   NonTerminal ::= an identifier that is not quoted
 *
 * `epsilon` is the spelling of an empty RHS. A rule may also be written
 * `A -> ;` to mean the same thing.
 */

export const EPSILON = "epsilon" as const;

export type SymbolKind = "nonterminal" | "terminal";

export interface GSymbol {
  readonly kind: SymbolKind;
  readonly name: string;
}

export interface Rule {
  /** Stable index assigned by the front-end; kernels reference rules by index. */
  readonly index: number;
  readonly lhs: string;
  readonly rhs: ReadonlyArray<GSymbol>;
  /** 1-based line in the source file, for diagnostics. */
  readonly line: number;
}

export interface Grammar {
  readonly start: string;
  readonly rules: ReadonlyArray<Rule>
  /** Non-terminal name -> rules with that left-hand side, in source order. */
  readonly byLhs: ReadonlyMap<string, ReadonlyArray<Rule>>;
  readonly nonterminals: ReadonlySet<string>;
  readonly terminals: ReadonlySet<string>;
  /** Set of non-terminals that admit at least one derivation to epsilon. */
  readonly nullable: ReadonlySet<string>;
}

export interface GrammarIssue {
  readonly severity: "error";
  readonly code:
    | "GRAMMAR_SYNTAX"
    | "GRAMMAR_DUPLICATE_START"
    | "GRAMMAR_UNDEFINED_NONTERMINAL"
    | "GRAMMAR_NO_RULES"
    | "GRAMMAR_START_WITHOUT_RULE";
  readonly message: string;
  readonly line: number;
  readonly column: number;
}

export class GrammarError extends Error {
  readonly issues: ReadonlyArray<GrammarIssue>;
  constructor(issues: ReadonlyArray<GrammarIssue>) {
    super(issues.map((i) => `line ${i.line}:${i.column} ${i.code} ${i.message}`).join("; "));
    this.name = "GrammarError";
    this.issues = issues;
  }
}
