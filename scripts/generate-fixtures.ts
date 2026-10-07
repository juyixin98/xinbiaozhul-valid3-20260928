/**
 * Generate a synthetic exhaustive short-string corpus for a given grammar.
 * Produces every token string over a terminal alphabet up to a length bound
 * and writes {input, accepted, count} rows to a JSON file. Counts come from
 * the independent reference (interval DP), so this file can cross-check the
 * Earley kernel without the kernel generating its own expectations.
 *
 * Usage:
 *   tsx scripts/generate-fixtures.ts --grammar fixtures/grammars/leftrec.bnf \
 *     --start E --alphabet 'id,+' --max-length 5 --out fixtures/expected/leftrec-exhaustive.json
 */

import { readFileSync, writeFileSync } from "node:fs";
import { parseArgs } from "node:util";
import { parseGrammar } from "../src/grammar/parser.js";
import { enumerateShortStrings, referenceCount } from "../src/reference/ref-brute.js";

const { values } = parseArgs({
  options: {
    grammar: { type: "string" },
    start: { type: "string", default: "S" },
    alphabet: { type: "string" },
    "max-length": { type: "string", default: "4" },
    out: { type: "string" },
  },
});

if (!values.grammar || !values.alphabet || !values.out) {
  process.stderr.write("requires --grammar, --alphabet and --out\n");
  process.exit(2);
}

const grammar = parseGrammar(readFileSync(values.grammar!, "utf8"), values.start!);
const alphabet = values.alphabet!.split(",").map((s) => s.trim()).filter(Boolean);
const maxLength = Number(values["max-length"]);
const strings = enumerateShortStrings(alphabet, maxLength);

const rows = strings.map((tokens) => {
  const ref = referenceCount(grammar, tokens);
  return {
    input: tokens,
    accepted: ref.count > 0n || ref.status === "infinite",
    status: ref.status,
    count: ref.count.toString(),
  };
});

writeFileSync(values.out!, JSON.stringify({ start: values.start, alphabet, maxLength, rows }, null, 2) + "\n");
process.stdout.write(`wrote ${rows.length} rows to ${values.out}\n`);
