import assert from "node:assert/strict";
import test from "node:test";
import { defaultConfig } from "../src/config.js";
import { recognizeString, redactForLog } from "../src/parser-service.js";
import { readFixture } from "./helpers.js";

test("accepted diagnostic carries request id and bounded chart state", async () => {
  const grammar = await readFixture("arithmetic.grammar");
  const response = recognizeString(
    grammar,
    { input: "id+id", requestId: "req-42" },
    defaultConfig,
    "E",
  );

  assert.equal(response.diagnostic.requestId, "req-42");
  assert.equal(response.diagnostic.outcome, "accepted");
  assert.equal(response.diagnostic.failureCategory, "none");
  assert.equal(response.diagnostic.keyState.acceptedStartItems, 1);
  assert.ok(response.diagnostic.keyState.chartSnapshots.length > 0);
});

test("lexical and syntactic failures have concrete categories", async () => {
  const grammar = await readFixture("arithmetic.grammar");
  const lexical = recognizeString(
    grammar,
    { input: "id?secret", requestId: "req-lex" },
    defaultConfig,
    "E",
  );
  const syntactic = recognizeString(
    grammar,
    { input: "id +", requestId: "req-syn" },
    defaultConfig,
    "E",
  );

  assert.equal(lexical.diagnostic.outcome, "rejected");
  assert.equal(lexical.diagnostic.failureCategory, "lexical");
  assert.equal(lexical.diagnostic.keyState.failureOffset, 2);
  assert.equal(syntactic.diagnostic.failureCategory, "syntactic");
  assert.equal(syntactic.diagnostic.keyState.failureOffset, 2);
});

test("log payload redacts input and terminal values", async () => {
  const grammar = "S -> 'secret'";
  const response = recognizeString(
    grammar,
    { input: "secret", requestId: "req-secret" },
    defaultConfig,
    "S",
  );
  const logged = JSON.stringify(redactForLog(response, defaultConfig));
  assert.ok(logged.includes(defaultConfig.diagnostics.mask));
  assert.ok(!logged.includes("secret"));
});
