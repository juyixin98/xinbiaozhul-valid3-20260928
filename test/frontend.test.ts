import { test } from "node:test";
import { strict as a } from "node:assert";
import { parseGrammar, computeNullable } from "../src/grammar/parser.js";
import { GrammarError } from "../src/grammar/types.js";
import { tokenizeInput } from "../src/grammar/lex-input.js";

test("parser expands alternatives and preserves duplicate productions", () => {
  const g = parseGrammar("S -> 'a' | 'a' ;", "S");
  a.equal(g.rules.length, 2);
  a.deepEqual(
    g.rules.map((r) => [r.lhs, r.rhs.map((s) => s.name)]),
    [
      ["S", ["a"]],
      ["S", ["a"]],
    ],
  );
});

test("parser records nullable closure for chained empty productions", () => {
  const g = parseGrammar("S -> A B ; A -> epsilon ; B -> C ; C -> ;", "S");
  a.deepEqual([...g.nullable].sort(), ["A", "B", "C", "S"]);
});

test("nullable fixed point terminates and flags cyclic nullable grammar", () => {
  const g = parseGrammar("S -> S | epsilon ;", "S");
  a.deepEqual([...g.nullable], ["S"]);
});

test("undefined non-terminal is a categorized grammar error", () => {
  a.throws(
    () => parseGrammar("S -> A ;", "S"),
    (err: unknown) => err instanceof GrammarError && err.issues[0]!.code === "GRAMMAR_UNDEFINED_NONTERMINAL",
  );
});

test("start symbol without a rule is a categorized grammar error", () => {
  a.throws(
    () => parseGrammar("A -> 'a' ;", "S"),
    (err: unknown) => err instanceof GrammarError && err.issues[0]!.code === "GRAMMAR_START_WITHOUT_RULE",
  );
});

test("syntax error reports line and column", () => {
  try {
    parseGrammar("S -> 'a' | @ ;", "S");
    a.fail("expected GrammarError");
  } catch (err) {
    a.ok(err instanceof GrammarError);
    a.equal(err.issues[0]!.code, "GRAMMAR_SYNTAX");
    a.equal(err.issues[0]!.line, 1);
  }
});

test("tokenizer handles whitespace and JSON array inputs", () => {
  a.deepEqual(tokenizeInput("id + id"), ["id", "+", "id"]);
  a.deepEqual(tokenizeInput('["id","*","id"]'), ["id", "*", "id"]);
  a.deepEqual(tokenizeInput("   "), []);
  a.throws(() => tokenizeInput("[1,2]"));
});

test("computeNullable returns empty set when nothing is nullable", () => {
  const g = parseGrammar("S -> 'a' S | 'a' ;", "S");
  a.equal(computeNullable(g.rules).size, 0);
});
