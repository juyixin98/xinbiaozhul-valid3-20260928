#!/usr/bin/env node
/**
 * Execution cross-check CLI: runs BOTH the Earley/SPPF pipeline and the
 * independent interval-DP reference over a corpus and reports discrepancies.
 *
 *   compare --corpus fixtures/expected/corpus.json
 *
 * Exit code 0 iff every expected verdict/count matches (infinite cases are
 * compared by status), 1 on any mismatch.
 */

import { readFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { parseArgs } from "node:util";
import { parseGrammar } from "../grammar/parser.js";
import { runEarley } from "../kernel/earley.js";
import { countParses } from "../forest/enumerate.js";
import { referenceCount } from "../reference/ref-brute.js";

interface CorpusCase {
  id: string;
  grammar: string;
  start: string;
  input: string[];
  expected: { verdict: "accept" | "reject"; status?: "exact" | "infinite"; count?: string };
}

function main(): void {
  const { values } = parseArgs({ options: { corpus: { type: "string" } } });
  if (!values.corpus) {
    process.stderr.write("missing --corpus\n");
    process.exit(2);
  }
  const corpusPath = resolve(values.corpus);
  const corpus = JSON.parse(readFileSync(corpusPath, "utf8")) as { cases: CorpusCase[] };
  let failures = 0;
  for (const c of corpus.cases) {
    const grammarPath = resolve(dirname(corpusPath), c.grammar);
    const grammar = parseGrammar(readFileSync(grammarPath, "utf8"), c.start);
    const run = runEarley(grammar, c.input);
    const ref = referenceCount(grammar, c.input);
    let sppf: { status: string; count: bigint } | null = null;
    if (run.result.verdict === "accept" && run.result.rootNodeId !== null) {
      sppf = countParses(run.forest, run.result.rootNodeId, { maxSteps: 10_000_000 });
    }
    const problems: string[] = [];
    if (run.result.verdict !== c.expected.verdict) {
      problems.push(`verdict ${run.result.verdict} != ${c.expected.verdict}`);
    }
    if (sppf && ref.status !== sppf.status) {
      problems.push(`status ref=${ref.status} sppf=${sppf.status}`);
    }
    if (sppf && ref.status === "exact" && ref.count !== sppf.count) {
      problems.push(`count ref=${ref.count} sppf=${sppf.count}`);
    }
    if (sppf && c.expected.status && sppf.status !== c.expected.status) {
      problems.push(`status sppf=${sppf.status} != expected ${c.expected.status}`);
    }
    if (sppf && c.expected.count !== undefined && sppf.status === "exact" && sppf.count.toString() !== c.expected.count) {
      problems.push(`count sppf=${sppf.count} != expected ${c.expected.count}`);
    }
    if (problems.length) {
      failures++;
      process.stdout.write(`FAIL ${c.id}: ${problems.join("; ")}\n`);
    } else {
      process.stdout.write(
        `PASS ${c.id}: ${run.result.verdict}/${sppf?.status ?? "-"}/${sppf?.count.toString() ?? "-"}\n`,
      );
    }
  }
  process.stdout.write(`\n${corpus.cases.length - failures}/${corpus.cases.length} cases agreed\n`);
  process.exit(failures === 0 ? 0 : 1);
}

main();
