import { test } from "node:test";
import { strict as a } from "node:assert";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parseGrammar } from "../src/grammar/parser.js";
import { runEarley } from "../src/kernel/earley.js";
import { countParses } from "../src/forest/enumerate.js";
import { enumerateShortStrings, referenceCount } from "../src/reference/ref-brute.js";

const load = (name: string, start: string) =>
  parseGrammar(readFileSync(resolve("fixtures/grammars", name), "utf8"), start);

/** Run both independent engines over one input and compare status/count. */
function crossCheck(grammarFile: string, start: string, input: string[]) {
  const g = load(grammarFile, start);
  const run = runEarley(g, input);
  const ref = referenceCount(g, input);
  const sppf =
    run.result.verdict === "accept" && run.result.rootNodeId !== null
      ? countParses(run.forest, run.result.rootNodeId, { maxSteps: 10_000_000 })
      : null;
  return { run, ref, sppf };
}

test("reference agrees with SPPF on hand-counted cases", () => {
  const cases: Array<[string, string, string[], bigint]> = [
    ["arith.bnf", "E", ["id"], 1n],
    ["arith.bnf", "E", ["id", "+", "id", "+", "id"], 2n],
    ["arith.bnf", "E", ["id", "+", "id", "*", "id"], 2n],
    ["arith.bnf", "E", ["id", "+", "id", "*", "id", "+", "id"], 5n],
    ["dup.bnf", "S", ["a"], 2n],
    ["leftrec.bnf", "E", ["id", "+", "id", "+", "id"], 1n],
    ["empty-chain.bnf", "S", [], 1n],
    ["paren.bnf", "S", ["(", ")"], 1n],
    ["binary.bnf", "E", ["(", "id", "+", "id", ")"], 1n],
  ];
  for (const [file, start, input, expected] of cases) {
    const { run, ref, sppf } = crossCheck(file, start, input);
    a.equal(run.result.verdict, "accept", `${file} ${JSON.stringify(input)}`);
    a.equal(ref.status, "exact", file);
    a.equal(ref.count, expected, `reference ${file}`);
    a.equal(sppf!.status, "exact", `sppf status ${file}`);
    a.equal(sppf!.count, expected, `sppf count ${file}`);
  }
});

test("exhaustive short strings: Earley verdict matches reference membership", () => {
  const g = load("leftrec.bnf", "E");
  const alphabet = ["id", "+"];
  const strings = enumerateShortStrings(alphabet, 5);
  let acceptedByBoth = 0;
  for (const tokens of strings) {
    const run = runEarley(g, tokens);
    const ref = referenceCount(g, tokens);
    const refAccept = ref.count > 0n && ref.status === "exact";
    a.equal(
      run.result.verdict === "accept",
      refAccept,
      `disagreement on ${JSON.stringify(tokens)}`,
    );
    if (refAccept) {
      a.equal(ref.count, 1n, "leftrec grammar is unambiguous");
      acceptedByBoth++;
    }
  }
  // Valid strings are id, id + id, id + id + id, ... (6 over length <= 5 tokens).
  a.equal(acceptedByBoth, 6);
});

test("exhaustive cross-check over the ambiguous arithmetic alphabet", () => {
  const g = load("arith.bnf", "E");
  const strings = enumerateShortStrings(["id", "+", "*"], 5);
  for (const tokens of strings) {
    const { run, ref, sppf } = (() => {
      const r = runEarley(g, tokens);
      const rf = referenceCount(g, tokens);
      const sp =
        r.result.verdict === "accept" && r.result.rootNodeId !== null
          ? countParses(r.forest, r.result.rootNodeId, { maxSteps: 10_000_000 })
          : null;
      return { run: r, ref: rf, sppf: sp };
    })();
    const refAccept = ref.count > 0n;
    a.equal(run.result.verdict === "accept", refAccept, JSON.stringify(tokens));
    if (sppf && ref.status === "exact") a.equal(sppf.count, ref.count, JSON.stringify(tokens));
  }
});

test("reference flags the cyclic grammar as infinite, matching SPPF", () => {
  const g = load("cyclic.bnf", "S");
  const ref = referenceCount(g, []);
  a.equal(ref.status, "infinite");
  const run = runEarley(g, []);
  const sppf = countParses(run.forest, run.result.rootNodeId!, { maxSteps: 1_000_000 });
  a.equal(sppf.status, "infinite");
});
