import assert from "node:assert/strict";
import test from "node:test";
import {
  EarleyRecognizer,
  compareWithIndependentEnumerator,
  parseGrammar,
  tokenize,
} from "../src/index.js";

const grammarSource = `
  S -> S S
  S -> 'a'
`;

function allStrings(alphabet: string[], length: number): string[] {
  if (length === 0) {
    return [""];
  }
  const shorter = allStrings(alphabet, length - 1);
  return alphabet.flatMap((letter) => shorter.map((prefix) => letter + prefix));
}

test("exhaustive short strings match independent top-down oracle", () => {
  const grammar = parseGrammar(grammarSource, "S");
  const inputs = [0, 1, 2, 3, 4].flatMap((length) => allStrings(["a", "b"], length));

  for (const input of inputs) {
    const tokens = tokenize(grammar, input);
    const recognition = new EarleyRecognizer().recognize(grammar, tokens);
    const report = compareWithIndependentEnumerator(grammar, tokens, recognition, {
      maxDerivations: 20,
      maxExpansions: 1000,
      maxDepth: 30,
    });

    assert.equal(report.referenceStatus, "complete", input);
    assert.equal(report.matched, true, `${input}: ${report.reason}`);
  }
});

test("oracle independently gives Catalan derivation counts for aaaa", () => {
  const grammar = parseGrammar(grammarSource, "S");
  const tokens = tokenize(grammar, "aaaa");
  const recognition = new EarleyRecognizer().recognize(grammar, tokens);
  const report = compareWithIndependentEnumerator(grammar, tokens, recognition, {
    maxDerivations: 20,
    maxExpansions: 5000,
    maxDepth: 30,
  });

  assert.equal(report.matched, true);
  assert.equal(report.accepted, true);
  assert.equal(report.earleyDerivationCount, 5);
  assert.equal(report.referenceDerivationCount, 5);
});
