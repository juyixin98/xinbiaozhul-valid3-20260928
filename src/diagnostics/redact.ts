/**
 * Redaction for potentially sensitive input.
 *
 * Inputs here are synthetic local fixtures, but the diagnostics pipeline is
 * built as if strings could contain secrets: a token is replaced by a stable
 * hash-based tag so diagnostics stay debuggable without printing raw values.
 */

import { createHash } from "node:crypto";

export function redactToken(token: string): string {
  if (token === "") return "<empty>";
  const digest = createHash("sha256").update(token).digest("hex").slice(0, 8);
  return `tok_${digest}`;
}

export function redactTokens(tokens: ReadonlyArray<string>): string[] {
  return tokens.map(redactToken);
}

export interface RedactionPolicy {
  /** When false, tokens are printed verbatim. Defaults to redacted. */
  reveal: boolean;
}

export function renderTokens(
  tokens: ReadonlyArray<string>,
  policy: RedactionPolicy = { reveal: false },
): string[] {
  return policy.reveal ? [...tokens] : redactTokens(tokens);
}
