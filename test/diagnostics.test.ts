import { test } from "node:test";
import { strict as a } from "node:assert";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parseAndDiagnose } from "../src/execution/parse-service.js";

const grammarSource = readFileSync(resolve("fixtures/grammars/arith.bnf"), "utf8");
const cyclicSource = readFileSync(resolve("fixtures/grammars/cyclic.bnf"), "utf8");
const emptySource = readFileSync(resolve("fixtures/grammars/empty-chain.bnf"), "utf8");

test("accepted diagnostic carries request id, key state and exact count", () => {
  const resp = parseAndDiagnose({
    requestId: "req-accepted-1",
    grammarSource,
    startSymbol: "E",
    input: "id + id + id",
  });
  const d = resp.diagnostic;
  a.equal(d.requestId, "req-accepted-1");
  a.equal(d.decision, "ACCEPTED");
  a.equal(d.enumeration.attempted, true);
  if (d.enumeration.attempted) a.equal(d.enumeration.count, "2");
  a.equal(d.keyState.chartSizes.length, 6);
  a.ok(d.keyState.chartSizes.every((n) => n > 0));
  a.equal(d.keyState.rootFamilies, 1);
  // Redacted by default: raw token never appears.
  a.ok(!JSON.stringify(d).includes("+"));
  a.ok(d.redactedInput.every((t) => t.startsWith("tok_")));
});

test("reveal policy prints raw tokens", () => {
  const resp = parseAndDiagnose({
    requestId: "r2",
    grammarSource,
    startSymbol: "E",
    input: "id + id",
    revealInput: true,
  });
  a.deepEqual(resp.diagnostic.redactedInput, ["id", "+", "id"]);
});

test("rejected diagnostic explains the missing completed start item", () => {
  const resp = parseAndDiagnose({
    requestId: "req-reject-1",
    grammarSource,
    startSymbol: "E",
    input: "id +",
  });
  const d = resp.diagnostic;
  a.equal(d.decision, "REJECTED");
  a.match(d.reason, /No completed item/);
  a.equal(d.enumeration.attempted, false);
  a.equal(d.keyState.rootNodeId, null);
});

test("infinite derivations map to an explicit undecidable decision", () => {
  const resp = parseAndDiagnose({
    requestId: "req-inf-1",
    grammarSource: cyclicSource,
    startSymbol: "S",
    input: "",
  });
  a.equal(resp.diagnostic.decision, "UNDECIDABLE_INFINITE_DERIVATIONS");
  if (resp.diagnostic.enumeration.attempted) a.equal(resp.diagnostic.enumeration.status, "infinite");
});

test("budget exhaustion maps to an undecidable decision but recognition stays accepted", () => {
  const resp = parseAndDiagnose({
    requestId: "req-budget-1",
    grammarSource,
    startSymbol: "E",
    input: "id + id * id + id",
    budget: { maxSteps: 2 },
  });
  a.equal(resp.run.result.verdict, "accept");
  a.equal(resp.diagnostic.decision, "UNDECIDABLE_ENUMERATION_BUDGET");
  a.match(resp.diagnostic.reason, /budget/);
});

test("empty-string accepted via chain has empty redacted input array", () => {
  const resp = parseAndDiagnose({
    requestId: "req-empty-1",
    grammarSource: emptySource,
    startSymbol: "S",
    input: "",
  });
  a.equal(resp.diagnostic.decision, "ACCEPTED");
  a.deepEqual(resp.diagnostic.redactedInput, []);
  if (resp.diagnostic.enumeration.attempted) a.equal(resp.diagnostic.enumeration.count, "1");
});
