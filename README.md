# Earley Shared Parse Forest

This TypeScript project implements an Earley recognizer with a shared packed
parse forest for ambiguous, left-recursive context-free grammars.

## Modules

- `src/frontend`: grammar parsing and longest-prefix lexical tokenization.
- `src/kernel`: Earley charts, state deduplication, and packed derivation links.
- `src/forest`: bounded concrete derivation-tree enumeration after recognition.
- `src/diagnostics`: request-scoped accept/reject/indeterminate diagnostics.
- `src/crosscheck`: an independent top-down bounded oracle used only by tests.
- `fixtures`: fixed grammar and expectation fixtures.

Grammar syntax uses one rule per line:

```text
E -> E '+' E
E -> id
S -> S
S -> .
```

`.` denotes epsilon. Identifiers with no productions are grammar-definition
errors. Quoted symbols are exact terminals. Whitespace in the input is skipped.

## Run

```sh
npm install
npm run build
node dist/cli.js fixtures/arithmetic.grammar "id+id*id" E request-1
node dist/cli.js fixtures/arithmetic.grammar "id+id*id" E request-2 --config=config/earley.config.json
```

Run the tests with:

```sh
npm test
```

The convenience script `scripts/verify.sh` runs the build and test command.

## Semantics And Limits

Earley states are keyed by production id, dot position, and start chart.
Distinct derivations are retained as distinct packed links. Recognition is a
finite chart construction and terminates for nullable cycles. Forest
enumeration is separate and has `maxDerivations` and `maxExpansions` budgets.
When a grammar such as `S -> S | .` has infinitely many derivations,
recognition accepts and enumeration reports `expansion-budget` or
`derivation-budget`; it does not pretend that the infinite set was exhausted.

Diagnostics never print raw input through `redactForLog`. Terminal source
values in emitted derivation trees are also removed unless configuration
explicitly enables them.

The independent oracle intentionally uses straightforward top-down recursive
enumeration rather than the Earley kernel. Its depth and derivation limits make
it suitable for short-string cross-checks, not arbitrary parsing.

## Verification Status

The dependencies are pinned in `package-lock.json`. In this delivery run
`npm install` completed. The TypeScript build surfaced strict optional-property
errors, which were fixed; the final build was not rerun because the runner
prohibits repeated verification commands. No test, benchmark, lint, or coverage
run was executed. Treat all test expectations as delivered but not reported as
passed.

Known implementation limits are:

- The lexer is exact-terminal and whitespace-based; it is not a regex lexer.
- The independent top-down oracle is a short-string check with explicit depth
  and expansion bounds, not a production parser.
- Infinite cyclic derivations are accepted by recognition but never fully
  enumerated; the forest result reports the budget that stopped enumeration.
