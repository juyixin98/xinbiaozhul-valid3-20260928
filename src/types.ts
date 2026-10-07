export interface ProductionRule {
  readonly id: string;
  readonly lhs: string;
  readonly rhs: readonly string[];
}

export interface Grammar {
  readonly startSymbol: string;
  readonly rules: readonly ProductionRule[];
  readonly nonterminals: ReadonlySet<string>;
  readonly terminals: ReadonlySet<string>;
  readonly productionsByLhs: ReadonlyMap<string, readonly ProductionRule[]>;
}

export interface InputToken {
  readonly kind: string;
  readonly value: string;
  readonly start: number;
  readonly end: number;
}

export class GrammarSyntaxError extends Error {
  constructor(
    message: string,
    readonly line?: number,
    readonly column?: number,
  ) {
    super(message);
    this.name = "GrammarSyntaxError";
  }
}

export class LexicalError extends Error {
  constructor(
    message: string,
    readonly position: number,
  ) {
    super(message);
    this.name = "LexicalError";
  }
}
