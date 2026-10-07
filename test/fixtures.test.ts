import { test } from "node:test";
import { strict as a } from "node:assert";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parseGrammar } from "../src/grammar/parser.js";
import { runEarley } from "../src/kernel/earley.js";
import { countParses } from "../src/forest/enumerate.js";

interface GeneratedRow {
  input: string[];
  accepted: boolean;
  status: "exact" | "infinite";
  count: string;
}

test("checked-in exhaustive fixture is reproduced by the Earley kernel", () => {
  const doc = JSON.parse(
    readFileSync(resolve("fixtures/expected/leftrec-exhaustive.json"), "utf8"),
  ) as { start: string; rows: GeneratedRow[] };
  const grammar = parseGrammar(readFileSync("fixtures/grammars/leftrec.bnf", "utf8"), doc.start);
  a.ok(doc.rows.length > 0);
  for (const row of doc.rows) {
    const run = runEarley(grammar, row.input);
    a.equal(run.result.verdict === "accept", row.accepted, JSON.stringify(row.input));
    if (run.result.verdict === "accept") {
      const c = countParses(run.forest, run.result.rootNodeId!, { maxSteps: 10_000_000 });
      a.equal(c.status, row.status);
      a.equal(c.count.toString(), row.count);
    }
  }
});

test("hand-computed corpus file is well-formed and asserts concrete counts", () => {
  const corpus = JSON.parse(
    readFileSync(resolve("fixtures/expected/corpus.json"), "utf8"),
  ) as { cases: Array<{ expected: { count?: string } }> };
  const withCounts = corpus.cases.filter((c) => c.expected.count !== undefined);
  a.ok(withCounts.length >= 6);
});
