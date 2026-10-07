import { randomUUID } from "node:crypto";
import type { EarleyConfig } from "../config.js";
import type { RecognitionResult } from "../kernel/earley.js";
import { GrammarSyntaxError, LexicalError } from "../types.js";

export type DiagnosticOutcome =
  | "accepted"
  | "rejected"
  | "indeterminate"
  | "invalid-request";

export type FailureCategory =
  | "none"
  | "lexical"
  | "syntactic"
  | "grammar-definition"
  | "internal";

export interface ParseRequest {
  requestId?: string;
  input: string;
}

export interface ChartSnapshot {
  chart: number;
  itemCount: number;
  items: Array<{
    ruleId: string;
    lhs: string;
    dottedRhs: string;
    start: number;
    derivationCount: number;
  }>;
}

export interface ParseDiagnostic {
  requestId: string;
  outcome: DiagnosticOutcome;
  failureCategory: FailureCategory;
  reason: string;
  redacted: boolean;
  inputLength: number;
  tokenCount: number;
  keyState: {
    charts: number;
    chartSnapshots: ChartSnapshot[];
    finalItemCount: number;
    acceptedStartItems: number;
    failureOffset?: number;
  };
}

function dottedRhs(result: RecognitionResult, ruleId: string, dot: number): string {
  const rule = result.grammar.rules.find((candidate) => candidate.id === ruleId);
  if (rule === undefined) {
    return "?";
  }
  const symbols = [...rule.rhs];
  symbols.splice(dot, 0, "•");
  return symbols.join(" ") || "•";
}

function snapshot(result: RecognitionResult, config: EarleyConfig): ChartSnapshot[] {
  if (!config.recognition.includeChartSnapshots) {
    return [];
  }

  return result.charts.map((chart) => ({
    chart: chart.index,
    itemCount: chart.items.length,
    items: chart.items.slice(0, config.recognition.maxDiagnosticItemsPerChart).map((item) => ({
      ruleId: item.rule.id,
      lhs: item.rule.lhs,
      dottedRhs: dottedRhs(result, item.rule.id, item.dot),
      start: item.start,
      derivationCount: item.links.length,
    })),
  }));
}

export function buildDiagnostic(
  request: ParseRequest,
  result: RecognitionResult | undefined,
  error: unknown,
  config: EarleyConfig,
): ParseDiagnostic {
  const requestId = request.requestId ?? randomUUID();

  if (error instanceof GrammarSyntaxError) {
    return {
      requestId,
      outcome: "invalid-request",
      failureCategory: "grammar-definition",
      reason: error.message,
      redacted: true,
      inputLength: request.input.length,
      tokenCount: 0,
      keyState: {
        charts: 0,
        chartSnapshots: [],
        finalItemCount: 0,
        acceptedStartItems: 0,
      },
    };
  }

  if (error instanceof LexicalError) {
    return {
      requestId,
      outcome: "rejected",
      failureCategory: "lexical",
      reason: error.message,
      redacted: config.diagnostics.redactInput,
      inputLength: request.input.length,
      tokenCount: 0,
      keyState: {
        charts: 0,
        chartSnapshots: [],
        finalItemCount: 0,
        acceptedStartItems: 0,
        failureOffset: error.position,
      },
    };
  }

  if (error instanceof Error) {
    return {
      requestId,
      outcome: "indeterminate",
      failureCategory: "internal",
      reason: error.message,
      redacted: true,
      inputLength: request.input.length,
      tokenCount: 0,
      keyState: {
        charts: 0,
        chartSnapshots: [],
        finalItemCount: 0,
        acceptedStartItems: 0,
      },
    };
  }

  if (result === undefined) {
    return {
      requestId,
      outcome: "indeterminate",
      failureCategory: "internal",
      reason: "No recognition result or error was supplied",
      redacted: true,
      inputLength: request.input.length,
      tokenCount: 0,
      keyState: {
        charts: 0,
        chartSnapshots: [],
        finalItemCount: 0,
        acceptedStartItems: 0,
      },
    };
  }

  const finalChart = result.charts[result.charts.length - 1]!;
  return {
    requestId,
    outcome: result.accepted ? "accepted" : "rejected",
    failureCategory: result.accepted ? "none" : "syntactic",
    reason: result.accepted
      ? "The complete input was consumed by a start-symbol derivation"
      : "No completed start item spans all token positions",
    redacted: config.diagnostics.redactInput,
    inputLength: request.input.length,
    tokenCount: result.tokens.length,
    keyState: {
      charts: result.charts.length,
      chartSnapshots: snapshot(result, config),
      finalItemCount: finalChart.items.length,
      acceptedStartItems: result.startItems.length,
      ...(result.accepted ? {} : { failureOffset: result.tokens.length }),
    },
  };
}
