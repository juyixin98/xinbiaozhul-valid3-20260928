import { defaultConfig, type EarleyConfig } from "./config.js";
import {
  buildDiagnostic,
  type ParseDiagnostic,
  type ParseRequest,
} from "./diagnostics/diagnostic-mapper.js";
import { enumerateForest, type ForestEnumeration } from "./forest/shared-forest.js";
import { parseGrammar } from "./frontend/grammar-parser.js";
import { tokenize } from "./frontend/tokenizer.js";
import { EarleyRecognizer, type RecognitionResult } from "./kernel/earley.js";
import type { DerivationTree } from "./forest/shared-forest.js";

export interface ParseResponse {
  diagnostic: ParseDiagnostic;
  recognition?: RecognitionResult;
  forest?: ForestEnumeration;
}

export function recognizeString(
  grammarSource: string,
  request: ParseRequest,
  config: EarleyConfig = defaultConfig,
  startSymbol = "S",
): ParseResponse {
  try {
    const grammar = parseGrammar(grammarSource, startSymbol);
    const tokens = tokenize(grammar, request.input);
    const recognition = new EarleyRecognizer().recognize(grammar, tokens);
    const diagnostic = buildDiagnostic(request, recognition, undefined, config);
    return {
      diagnostic,
      recognition,
      forest: enumerateForest(recognition, config.forest),
    };
  } catch (error) {
    return { diagnostic: buildDiagnostic(request, undefined, error, config) };
  }
}

export function redactForLog(
  response: ParseResponse,
  config: EarleyConfig = defaultConfig,
): Record<string, unknown> {
  const redactTree = (tree: DerivationTree): DerivationTree => {
    const children = tree.children.map(redactTree);
    if (tree.terminal !== undefined) {
      return {
        symbol: "<terminal>",
        ruleId: "<terminal>",
        start: tree.start,
        end: tree.end,
        children: [],
        terminal: { tokenIndex: tree.terminal.tokenIndex, kind: "<terminal>", value: config.diagnostics.mask },
      };
    }
    return { ...tree, children };
  };

  const payload: Record<string, unknown> = {
    requestId: response.diagnostic.requestId,
    outcome: response.diagnostic.outcome,
    failureCategory: response.diagnostic.failureCategory,
    reason: response.diagnostic.reason,
    keyState: config.diagnostics.redactInput
      ? {
          ...response.diagnostic.keyState,
          chartSnapshots: response.diagnostic.keyState.chartSnapshots.map((chart) => ({
            ...chart,
            items: chart.items.map((item) => ({ ...item, dottedRhs: config.diagnostics.mask })),
          })),
        }
      : response.diagnostic.keyState,
  };

  if (config.diagnostics.redactInput) {
    payload.input = config.diagnostics.mask;
  }
  if (response.forest !== undefined) {
    payload.forest = {
      status: response.forest.status,
      derivationCount: response.forest.derivationCount,
      expansionsUsed: response.forest.expansionsUsed,
      budgets: response.forest.budgets,
      derivations: config.diagnostics.showTerminalValues
        ? response.forest.derivations
        : response.forest.derivations.map(redactTree),
    };
  }

  return payload;
}
