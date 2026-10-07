import assert from "node:assert/strict";
import test from "node:test";
import { join } from "node:path";
import { fixtureDirectory, readFixture, parseWithCount } from "./helpers.js";

test("hand-computed arithmetic ambiguity counts", async () => {
  const source = await readFixture("arithmetic.grammar");
  const expected = JSON.parse(
    await readFile(join(fixtureDirectory, "expected-results.json")),
  ) as { arithmetic: Record<string, number> };
  const cases = Object.entries(expected.arithmetic);

  for (const [input, expectedCount] of cases) {
    const { forest, recognition } = parseWithCount(source, input, "E");
    assert.equal(recognition.accepted, true, input);
    assert.equal(forest.status, "complete", input);
    assert.equal(forest.derivationCount, expectedCount, input);
  }
});

test("empty input has one explicit epsilon derivation", async () => {
  const source = await readFixture("multi-derivation.grammar");
  const { forest } = parseWithCount(source, "", "S");
  assert.equal(forest.status, "complete");
  assert.equal(forest.derivationCount, 2);
});

test("state deduping preserves two distinct completed links", async () => {
  const source = `
    S -> A B
    A -> .
    B -> 'x'
    S -> 'x'
  `;
  const { forest, recognition } = parseWithCount(source, "x", "S");
  assert.equal(recognition.accepted, true);
  assert.equal(forest.derivationCount, 2);
});

test("left recursive epsilon closure terminates and enumeration is separately budgeted", async () => {
  const source = await readFixture("left-recursive-epsilon.grammar");
  const { recognition, forest } = parseWithCount(source, "", "S", {
    maxDerivations: 50,
    maxExpansions: 50,
  });
  assert.equal(recognition.accepted, true);
  assert.equal(recognition.charts.length, 1);
  assert.equal(forest.status, "expansion-budget");
  assert.ok(forest.expansionsUsed >= 50);
});

test("rejected input is separate from forest enumeration", async () => {
  const source = await readFixture("arithmetic.grammar");
  const { recognition, forest } = parseWithCount(source, "id+", "E");
  assert.equal(recognition.accepted, false);
  assert.equal(forest.derivationCount, 0);
  assert.equal(forest.status, "complete");
});
