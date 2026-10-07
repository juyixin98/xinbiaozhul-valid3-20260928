import { test } from "node:test";
import { strict as a } from "node:assert";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parseGrammar } from "../src/grammar/parser.js";
import { runEarley } from "../src/kernel/earley.js";
import { countParses } from "../src/forest/enumerate.js";

const fixture = (name: string) => resolve("fixtures/grammars", name);
const load = (name: string, start: string) =>
  parseGrammar(readFileSync(fixture(name), "utf8"), start);

function countOf(grammarFile: string, start: string, input: string[], maxSteps = 1_000_000) {
  const g = load(grammarFile, start);
  const run = runEarley(g, input);
  a.equal(run.result.verdict, "accept");
  a.ok(run.result.rootNodeId !== null);
  return { run, count: countParses(run.forest, run.result.rootNodeId!, { maxSteps }) };
}

test("hand-counted ambiguous expression counts (Catalan)", () => {
  a.equal(countOf("arith.bnf", "E", ["id"]).count.count, 1n);
  a.equal(countOf("arith.bnf", "E", ["id", "+", "id", "+", "id"]).count.count, 2n);
  a.equal(countOf("arith.bnf", "E", ["id", "+", "id", "*", "id"]).count.count, 2n);
  a.equal(countOf("arith.bnf", "E", ["id", "+", "id", "*", "id", "+", "id"]).count.count, 5n);
});

test("identical productions produce two derivations (de-dup keeps both)", () => {
  const { count } = countOf("dup.bnf", "S", ["a"]);
  a.equal(count.status, "exact");
  a.equal(count.count, 2n);
});

test("direct left recursion terminates and stays unambiguous", () => {
  const { count } = countOf("leftrec.bnf", "E", ["id", "+", "id", "+", "id"]);
  a.equal(count.count, 1n);
});

test("empty production closure terminates on the empty string", () => {
  const { run, count } = countOf("empty-chain.bnf", "S", []);
  a.equal(count.status, "exact");
  a.equal(count.count, 1n);
  // The closure in S0 reaches the completed start item.
  a.ok(run.chart[0]!.items.some((it) => it.dot === 1 && it.origin === 0));
});

test("cyclic nullable grammar accepts but enumeration reports infinite", () => {
  const g = load("cyclic.bnf", "S");
  const run = runEarley(g, []);
  a.equal(run.result.verdict, "accept");
  const count = countParses(run.forest, run.result.rootNodeId!, { maxSteps: 1_000_000 });
  a.equal(count.status, "infinite");
});

test("closure is bounded: cyclic grammar produces a finite number of items", () => {
  const g = load("cyclic.bnf", "S");
  const run = runEarley(g, []);
  a.ok(Number.isFinite(run.result.itemCount));
  a.ok(run.result.itemCount < 50, "closure should reach fixed point quickly");
  a.equal(run.result.setCount, 1);
});

test("rejected input has no root families and a NO_PARSE reason", () => {
  const g = load("arith.bnf", "E");
  const run = runEarley(g, ["id", "+"]);
  a.equal(run.result.verdict, "reject");
  a.equal(run.result.rejectReason, "NO_PARSE");
  a.equal(run.result.rootNodeId, null);
});

test("unambiguous parenthesis grammar counts one per valid string", () => {
  a.equal(countOf("paren.bnf", "S", ["(", ")"]).count.count, 1n);
  a.equal(countOf("paren.bnf", "S", ["(", ")", "(", ")"]).count.count, 1n);
  a.equal(runEarley(load("paren.bnf", "S"), ["("]).result.verdict, "reject");
});

test("budget exhaustion is separated from recognition", () => {
  // A genuinely finite but larger ambiguous string: force a tiny budget.
  const g = load("arith.bnf", "E");
  const input = ["id", "+", "id", "*", "id", "+", "id"];
  const run = runEarley(g, input);
  a.equal(run.result.verdict, "accept");
  const count = countParses(run.forest, run.result.rootNodeId!, { maxSteps: 2 });
  a.equal(count.status, "budget_exhausted");
  // Recognition was still a success despite enumeration failing.
  a.ok(run.result.rootNodeId !== null);
});

test("SPPF shares sub-forest nodes: id leaves are reused across parses", () => {
  const g = load("arith.bnf", "E");
  const run = runEarley(g, ["id", "+", "id", "+", "id"]);
  const terminalNodes = run.forest.nodes.filter((n) => n.kind === "terminal" && n.label === "id");
  a.equal(terminalNodes.length, 3, "one terminal node per span, shared");
});
