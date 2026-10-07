import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  EarleyRecognizer,
  enumerateForest,
  parseGrammar,
  tokenize,
} from "../src/index.js";

export const fixtureDirectory = join(dirname(fileURLToPath(import.meta.url)), "..", "fixtures");

export async function readFixture(name: string): Promise<string> {
  return await readFile(join(fixtureDirectory, name), "utf8");
}

export function parseWithCount(
  grammarSource: string,
  input: string,
  startSymbol = "S",
  budgets = { maxDerivations: 1000, maxExpansions: 5000 },
) {
  const grammar = parseGrammar(grammarSource, startSymbol);
  const tokens = tokenize(grammar, input);
  const recognition = new EarleyRecognizer().recognize(grammar, tokens);
  const forest = enumerateForest(recognition, budgets);
  return { grammar, tokens, recognition, forest };
}

export function assertTreeShape(tree: unknown, expected: unknown): void {
  const stripValues = (value: unknown): unknown => {
    if (Array.isArray(value)) {
      return value.map(stripValues);
    }
    if (value && typeof value === "object") {
      return Object.fromEntries(
        Object.entries(value)
          .filter(([key]) => key !== "value")
          .map(([key, child]) => [key, stripValues(child)]),
      );
    }
    return value;
  };
  assert.deepEqual(stripValues(tree), expected);
}
