import assert from "node:assert/strict";
import test from "node:test";
import { LexicalError, GrammarSyntaxError, parseGrammar, tokenize } from "../src/index.js";
import { readFixture } from "./helpers.js";

test("grammar parser accepts epsilon and exposes terminal/nonterminal sets", async () => {
  const source = await readFixture("left-recursive-epsilon.grammar");
  const grammar = parseGrammar(source, "S");
  assert.deepEqual([...grammar.nonterminals], ["S"]);
  assert.deepEqual(grammar.rules.map((rule) => rule.rhs.length), [1, 0]);
});

test("tokenizer reports lexical failure with concrete offset", async () => {
  const grammar = parseGrammar(await readFixture("arithmetic.grammar"), "E");
  assert.throws(
    () => tokenize(grammar, "id?"),
    (error: unknown) =>
      error instanceof LexicalError &&
      error.position === 2 &&
      error.message.includes("offset 2"),
  );
});

test("undefined nonterminal is a grammar-definition failure", () => {
  assert.throws(
    () => parseGrammar("S -> A", "S"),
    (error: unknown) => error instanceof GrammarSyntaxError,
  );
});
