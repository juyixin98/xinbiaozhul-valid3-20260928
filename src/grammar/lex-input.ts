/**
 * Input front-end.
 *
 * Two input modes are supported:
 *
 * 1. Whitespace-separated token names, e.g. `id + id`. Every token must name a
 *    grammar terminal; this keeps tests completely explicit.
 * 2. A JSON array of strings on stdin/file for fixtures, e.g. ["id","+","id"].
 */
export function tokenizeInput(text: string): string[] {
  const trimmed = text.trim();
  if (trimmed.startsWith("[")) {
    let parsed: unknown;
    try {
      parsed = JSON.parse(trimmed);
    } catch {
      throw new Error("input looked like a JSON array but failed to parse");
    }
    if (!Array.isArray(parsed) || parsed.some((t) => typeof t !== "string")) {
      throw new Error("JSON input must be an array of strings");
    }
    return parsed as string[];
  }
  if (trimmed === "") return [];
  return trimmed.split(/\s+/);
}
