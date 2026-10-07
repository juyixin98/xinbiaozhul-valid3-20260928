import type { Grammar, InputToken } from "../types.js";
import { LexicalError } from "../types.js";

function isWhitespace(codePoint: number | undefined): boolean {
  return codePoint !== undefined && /\s/u.test(String.fromCodePoint(codePoint));
}

export function tokenize(grammar: Grammar, input: string): InputToken[] {
  const terminals = [...grammar.terminals].sort((a, b) => b.length - a.length);
  const tokens: InputToken[] = [];
  let position = 0;

  while (position < input.length) {
    const codePoint = input.codePointAt(position);
    if (isWhitespace(codePoint)) {
      position += codePoint === undefined ? 1 : (codePoint > 0xffff ? 2 : 1);
      continue;
    }

    const match = terminals.find((terminal) => input.startsWith(terminal, position));
    if (match === undefined) {
      throw new LexicalError(`No terminal starts at offset ${position}`, position);
    }

    tokens.push({
      kind: match,
      value: input.slice(position, position + match.length),
      start: position,
      end: position + match.length,
    });
    position += match.length;
  }

  return tokens;
}
