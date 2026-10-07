# Earley Recognizer + Shared Packed Parse Forest

TypeScript / Node implementation of an Earley recognizer that handles direct
and indirect left recursion, empty productions and ambiguous grammars, and
builds a Shared Packed Parse Forest (SPPF). Recognition and forest
enumeration are separate phases: enumeration has an explicit step budget and
can report infinite derivations.

## Module layout

- `src/grammar/` language front-end: BNF lexer/parser, nullability closure,
  input tokenizer (`types.ts`, `parser.ts`, `lex-input.ts`).
- `src/kernel/` Earley deduction kernel (`earley.ts`) and result types.
- `src/forest/` SPPF builder (`sppf.ts`) and budgeted enumeration
  (`enumerate.ts`): exact bigint counts, tree materialization, cycle detection.
- `src/diagnostics/` diagnostic mapping (`report.ts`) and input redaction
  (`redact.ts`): structured accept/reject/undecidable records with request id.
- `src/reference/` independent reference (`ref-brute.ts`): interval dynamic
  programming that shares no code with the kernel, plus short-string
  enumeration for exhaustive cross-checks.
- `src/execution/` orchestration service (`parse-service.ts`).
- `src/cli/` entry points `recognize.ts` and `compare.ts`.

## Grammar dialect

```
S -> E ;
E -> E '+' E | E '*' E | 'id' ;
A -> epsilon ;   # empty RHS; `A -> ;` means the same
```

Terminals are single/double quoted strings; everything else is a
non-terminal. Duplicate productions are preserved (they are real ambiguity).
`#` starts a line comment.

## Boundary semantics

- Termination: chart sets are item sets keyed by
  `(rule, dot, origin, parse-node)`. Prediction and zero-width completion run
  in a work queue with expecter/completion registries wired in both
  directions. The item universe is finite, so nullable closures (including
  `S -> S | epsilon` and mutual chains) always reach a fixed point.
- De-duplication vs ambiguity: two states merge only when they reference the
  same SPPF node (same label/span AND same child families). Distinct
  productions and distinct bracketings create distinct nodes/families, so
  `S -> 'a' | 'a'` counts 2 and `id + id + id` counts 2.
- Recognition vs enumeration: recognition only requires a start symbol node
  `(start, 0, n)` with at least one family. Counting walks the forest under a
  budget. Results: `exact` (bigint count), `budget_exhausted` (lower bound),
  or `infinite` (a reachable zero-width recursive cycle, e.g. cyclic
  nullable grammars). A budget failure never revokes a successful recognition.
- Infinite derivations: a grammar can be accepted but have no finite parse
  count. The independent reference detects this structurally from a cycle in
  the nullable dependency graph; the enumerator detects it with gray-node
  coloring. Both must agree.
- The reference counter is an independent interval-DP algorithm, not derived
  from the kernel. Hand counts live in `fixtures/expected/handcounts.md`.
- Diagnostics print redacted input tokens (stable `sha256` tags) unless
  `--reveal` is passed.

## Run

Requires Node.js >= 18.19 and npm. Dependencies are pinned in `package.json`.

```sh
npm install
npm run build          # tsc type-check / emit to dist/

# Recognize a single input (prints a JSON diagnostic record)
npx tsx src/cli/recognize.ts --grammar fixtures/grammars/arith.bnf \
  --start E --input "id + id * id" --request-id req-42

# Cross-check the hand-computed corpus across BOTH engines
npx tsx src/cli/compare.ts --corpus fixtures/expected/corpus.json

# Regenerate an exhaustive short-string fixture from the reference
npm run fixtures

# Test suite (Node built-in test runner)
npm test

# Full shell verification (fixture regen + compare + tests)
bash scripts/verify.sh
```

`recognize` exit codes: `0` accepted (or enumeration undecidable while
recognized), `1` rejected, `2` grammar/usage error.

## Tests

- `test/frontend.test.ts` parser, duplicate productions, nullable closure,
  categorized grammar errors, tokenizer.
- `test/kernel.test.ts` hand-counted Catalan ambiguity, duplicate
  derivations, left recursion, empty-string and cyclic grammar termination,
  rejection category, budget separation, forest sharing.
- `test/reference.test.ts` independent agreement plus exhaustive short-string
  cross-checks over `id/+/*`.
- `test/diagnostics.test.ts` request id, key state, redaction, and each
  decision category (accept / reject / infinite / budget).
- `test/fixtures.test.ts` replays the checked-in generated fixture.

## Known limitations

- No error recovery beyond a single verdict; malformed input strings are a
  hard reject.
- Tree materialization is intended for small forests; counting scales to
  much larger forests but still needs a budget.
- The interval-DP reference is exponential in RHS length (it enumerates all
  splits) and is meant for small fixture grammars, not production inputs.
- Checks documented here were type-checked and smoke-run once during
  delivery; the repository runner did not execute the test suite, so passing
  `npm test` is not claimed.
