/** High-level orchestration tying front-end, kernel, forest and diagnostics together. */

import { parseGrammar } from "../grammar/parser.js";
import { tokenizeInput } from "../grammar/lex-input.js";
import { runEarley } from "../kernel/earley.js";
import { countParses, type EnumerationBudget } from "../forest/enumerate.js";
import { buildDiagnostic, type DiagnosticRecord } from "../diagnostics/report.js";
import type { Grammar } from "../grammar/types.js";

export interface ServiceRequest {
  requestId: string;
  grammarSource: string;
  input: string;
  startSymbol?: string;
  budget?: EnumerationBudget;
  /** Print raw tokens in diagnostics. Defaults to redacted. */
  revealInput?: boolean;
}

export interface ServiceResponse {
  grammar: Grammar;
  tokens: string[];
  run: ReturnType<typeof runEarley>;
  count: ReturnType<typeof countParses> | null;
  diagnostic: DiagnosticRecord;
}

export function parseAndDiagnose(req: ServiceRequest): ServiceResponse {
  const grammar = parseGrammar(req.grammarSource, req.startSymbol ?? "S");
  const tokens = tokenizeInput(req.input);
  const run = runEarley(grammar, tokens);
  const budget = req.budget ?? { maxSteps: 1_000_000 };
  const count =
    run.result.verdict === "accept" && run.result.rootNodeId !== null
      ? countParses(run.forest, run.result.rootNodeId, budget)
      : null;
  const diagnostic = buildDiagnostic(
    run,
    tokens,
    { requestId: req.requestId, redaction: { reveal: req.revealInput ?? false } },
    count,
    budget.maxSteps,
  );
  return { grammar, tokens, run, count, diagnostic };
}
