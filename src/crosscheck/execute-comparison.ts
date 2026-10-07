import type { Grammar, InputToken } from "../types.js";
import { enumerateForest } from "../forest/shared-forest.js";
import type { RecognitionResult } from "../kernel/earley.js";
import {
  enumerateByTopDownExpansion,
  type ReferenceResult,
} from "./reference-enumerator.js";

export interface ComparisonBudgets {
  maxDerivations: number;
  maxExpansions: number;
  maxDepth: number;
}

export interface ComparisonReport {
  matched: boolean;
  accepted: boolean;
  earleyDerivationCount: number;
  referenceDerivationCount: number;
  referenceStatus: ReferenceResult["status"];
  reason: string;
}

function treeShape(tree: {
  symbol: string;
  ruleId: string;
  start: number;
  end: number;
  children: unknown[];
  terminal?: unknown;
}): string {
  return JSON.stringify(tree, (key, value) => {
    if (key === "value") {
      return undefined;
    }
    return value;
  });
}

export function compareWithIndependentEnumerator(
  grammar: Grammar,
  tokens: readonly InputToken[],
  recognition: RecognitionResult,
  budgets: ComparisonBudgets,
): ComparisonReport {
  const forest = enumerateForest(recognition, budgets);
  const reference = enumerateByTopDownExpansion(grammar, tokens, budgets);

  if (reference.status !== "complete") {
    return {
      matched: false,
      accepted: recognition.accepted,
      earleyDerivationCount: forest.derivationCount,
      referenceDerivationCount: reference.derivations.length,
      referenceStatus: reference.status,
      reason: "Reference enumerator reached its bound, so comparison is not a complete oracle",
    };
  }

  const earleyShapes = forest.derivations.map(treeShape).sort();
  const referenceShapes = reference.derivations.map(treeShape).sort();
  const sameSet =
    earleyShapes.length === referenceShapes.length &&
    earleyShapes.every((shape, index) => shape === referenceShapes[index]);

  return {
    matched:
      sameSet &&
      forest.status === "complete" &&
      recognition.accepted === (reference.derivations.length > 0),
    accepted: recognition.accepted,
    earleyDerivationCount: forest.derivationCount,
    referenceDerivationCount: reference.derivations.length,
    referenceStatus: reference.status,
    reason:
      sameSet && forest.status === "complete"
        ? "Earley accepted/rejected and derivation multiset matched the independent top-down oracle"
        : "Earley result differs from the independent top-down oracle",
  };
}
