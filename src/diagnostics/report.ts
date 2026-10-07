/**
 * Diagnostic mapping: turns a kernel run + enumeration outcome into a single
 * structured record explaining WHY the input was accepted, rejected, or could
 * not be decided, including key state (chart sizes, final-set items, budget).
 *
 * Every record carries a request id. Raw tokens are redacted by default.
 */

import type { Grammar } from "../grammar/types.js";
import type { RunOutput } from "../kernel/earley.js";
import type { CountResult } from "../forest/enumerate.js";
import { renderTokens, type RedactionPolicy } from "./redact.js";

export type Decision =
  | "ACCEPTED"
  | "REJECTED"
  | "UNDECIDABLE_ENUMERATION_BUDGET"
  | "UNDECIDABLE_INFINITE_DERIVATIONS";

export interface DiagnosticRecord {
  requestId: string;
  decision: Decision;
  reason: string;
  grammarStart: string;
  inputLength: number;
  redactedInput: string[];
  keyState: {
    chartSizes: number[];
    totalItems: number;
    finalSetCompletedLhs: string[];
    rootNodeId: number | null;
    rootFamilies: number | null;
  };
  enumeration:
    | { attempted: false }
    | {
        attempted: true;
        status: CountResult["status"];
        count: string;
        stepsUsed: number;
        maxSteps: number;
      };
}

export interface DiagnosticOptions {
  requestId: string;
  redaction?: RedactionPolicy;
}

export function buildDiagnostic(
  run: RunOutput,
  tokens: ReadonlyArray<string>,
  options: DiagnosticOptions,
  count: CountResult | null,
  budgetMaxSteps: number | null,
): DiagnosticRecord {
  const { result, forest, chart, grammar } = run;
  const finalSet = chart[chart.length - 1]!;
  const completedLhs = [
    ...new Set(
      finalSet.items
        .filter((it) => it.dot === (grammar.rules[it.ruleIndex]?.rhs.length ?? -1))
        .map((it) => grammar.rules[it.ruleIndex]!.lhs),
    ),
  ];

  const rootFamilies =
    result.rootNodeId === null ? null : forest.get(result.rootNodeId).families.length;

  let decision: Decision;
  let reason: string;
  if (result.verdict === "reject") {
    decision = "REJECTED";
    reason =
      `No completed item for start symbol '${grammar.start}' spanning 0..${tokens.length} ` +
      `exists in the final chart set. The final set only completed: ` +
      `${completedLhs.length ? completedLhs.join(", ") : "<nothing>"}.`;
  } else if (count?.status === "budget_exhausted") {
    decision = "UNDECIDABLE_ENUMERATION_BUDGET";
    reason =
      `Recognized, but enumeration used ${count.stepsUsed} steps against a budget of ` +
      `${budgetMaxSteps} before finishing; the exact parse count is not determined.`;
  } else if (count?.status === "infinite") {
    decision = "UNDECIDABLE_INFINITE_DERIVATIONS";
    reason =
      "Recognized, but the forest contains a zero-width recursive cycle, so the " +
      "grammar admits infinitely many derivations; no finite parse count exists.";
  } else {
    decision = "ACCEPTED";
    reason = `A completed start item for '${grammar.start}' spans 0..${tokens.length}.`;
  }

  const enumeration: DiagnosticRecord["enumeration"] = count
    ? {
        attempted: true,
        status: count.status,
        count: count.count.toString(),
        stepsUsed: count.stepsUsed,
        maxSteps: budgetMaxSteps ?? -1,
      }
    : { attempted: false };

  return {
    requestId: options.requestId,
    decision,
    reason,
    grammarStart: grammar.start,
    inputLength: tokens.length,
    redactedInput: renderTokens(tokens, options.redaction ?? { reveal: false }),
    keyState: {
      chartSizes: chart.map((s) => s.items.length),
      totalItems: result.itemCount,
      finalSetCompletedLhs: completedLhs,
      rootNodeId: result.rootNodeId,
      rootFamilies,
    },
    enumeration,
  };
}

export function summarizeGrammar(grammar: Grammar): { rules: number; nonterminals: number; nullable: string[] } {
  return {
    rules: grammar.rules.length,
    nonterminals: grammar.nonterminals.size,
    nullable: [...grammar.nullable],
  };
}
