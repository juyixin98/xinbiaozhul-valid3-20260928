#!/usr/bin/env node
/**
 * recognize CLI:
 *   recognize --grammar file.bnf --input "id + id" [--start S]
 *              [--budget 1000000] [--reveal] [--request-id id] [--file input.txt]
 *
 * Prints one JSON diagnostic record on stdout. Exit code:
 *   0 accepted or undecidable enumeration (recognition succeeded)
 *   1 rejected
 *   2 grammar/usage error
 */

import { readFileSync } from "node:fs";
import { parseArgs } from "node:util";
import { randomUUID } from "node:crypto";
import { parseAndDiagnose } from "../execution/parse-service.js";

function main(): void {
  const { values } = parseArgs({
    options: {
      grammar: { type: "string" },
      input: { type: "string", default: "" },
      file: { type: "string" },
      start: { type: "string", default: "S" },
      budget: { type: "string", default: "1000000" },
      reveal: { type: "boolean", default: false },
      "request-id": { type: "string", default: randomUUID() },
    },
  });
  if (!values.grammar) {
    process.stderr.write("missing --grammar\n");
    process.exit(2);
  }
  const grammarSource = readFileSync(values.grammar, "utf8");
  const input = values.file ? readFileSync(values.file, "utf8") : (values.input ?? "");
  try {
    const resp = parseAndDiagnose({
      requestId: values["request-id"]!,
      grammarSource,
      input,
      startSymbol: values.start!,
      budget: { maxSteps: Number(values.budget) },
      revealInput: values.reveal,
    });
    process.stdout.write(JSON.stringify(resp.diagnostic, null, 2) + "\n");
    process.exit(resp.diagnostic.decision === "REJECTED" ? 1 : 0);
  } catch (err) {
    process.stderr.write(String(err) + "\n");
    process.exit(2);
  }
}

main();
