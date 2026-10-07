import type { Grammar, InputToken, ProductionRule } from "../types.js";

export type PackedLink =
  | { kind: "epsilon" }
  | { kind: "scan"; waiterItemId: number; tokenIndex: number }
  | { kind: "complete"; waiterItemId: number; completedItemId: number };

export interface EarleyItem {
  readonly id: number;
  rule: ProductionRule;
  dot: number;
  start: number;
  end: number;
  links: PackedLink[];
}

export interface Chart {
  readonly index: number;
  readonly items: EarleyItem[];
  readonly itemByKey: Map<string, EarleyItem>;
}

export interface RecognitionResult {
  readonly accepted: boolean;
  readonly charts: Chart[];
  readonly startItems: readonly EarleyItem[];
  readonly tokens: readonly InputToken[];
  readonly grammar: Grammar;
}

export function itemKey(ruleId: string, dot: number, start: number): string {
  return `${ruleId}@${dot}:${start}`;
}

function makeChart(index: number): Chart {
  return { index, items: [], itemByKey: new Map() };
}

export class EarleyRecognizer {
  private nextItemId = 0;

  recognize(grammar: Grammar, tokens: readonly InputToken[]): RecognitionResult {
    const charts = tokens.map((_, index) => makeChart(index));
    charts.push(makeChart(tokens.length));

    const seedRules = grammar.productionsByLhs.get(grammar.startSymbol) ?? [];
    for (const rule of seedRules) {
      this.addItem(charts[0]!, rule, 0, 0, { kind: "epsilon" });
    }

    for (let chartIndex = 0; chartIndex <= tokens.length; chartIndex += 1) {
      const chart = charts[chartIndex]!;
      const completionQueue: EarleyItem[] = [];
      const scheduledCompletions = new Set<number>();
      let cursor = 0;
      while (cursor < chart.items.length) {
        const item = chart.items[cursor]!;
        cursor += 1;
        if (item.dot === item.rule.rhs.length) {
          if (!scheduledCompletions.has(item.id)) {
            scheduledCompletions.add(item.id);
            completionQueue.push(item);
          }
        } else {
          this.processIncompleteItem(
            grammar,
            charts,
            chart,
            item,
            tokens,
            completionQueue,
            scheduledCompletions,
          );
        }

        while (completionQueue.length > 0) {
          const completed = completionQueue.shift()!;
          scheduledCompletions.delete(completed.id);
          const linkVersion = completed.links.length;
          this.processCompletion(
            charts,
            chart,
            completed,
            completionQueue,
            scheduledCompletions,
          );
          if (completed.links.length > linkVersion && !scheduledCompletions.has(completed.id)) {
            scheduledCompletions.add(completed.id);
            completionQueue.push(completed);
          }
        }
      }
    }

    const finalChart = charts[tokens.length]!;
    const startItems = finalChart.items.filter(
      (item) =>
        item.rule.lhs === grammar.startSymbol &&
        item.dot === item.rule.rhs.length &&
        item.start === 0,
    );

    return {
      accepted: startItems.length > 0,
      charts,
      startItems,
      tokens,
      grammar,
    };
  }

  private processIncompleteItem(
    grammar: Grammar,
    charts: Chart[],
    chart: Chart,
    item: EarleyItem,
    tokens: readonly InputToken[],
    completionQueue: EarleyItem[],
    scheduledCompletions: Set<number>,
  ): void {
    const nextSymbol = item.rule.rhs[item.dot];
    if (nextSymbol === undefined) {
      return;
    }

    if (grammar.nonterminals.has(nextSymbol)) {
      this.predict(grammar, chart, nextSymbol);
      this.completeWithExistingNullable(
        chart,
        item,
        nextSymbol,
        completionQueue,
        scheduledCompletions,
      );
      return;
    }

    this.scan(charts, chart.index, item, nextSymbol, tokens);
  }

  private predict(grammar: Grammar, chart: Chart, symbol: string): void {
    const rules = grammar.productionsByLhs.get(symbol) ?? [];
    for (const rule of rules) {
      this.addItem(chart, rule, 0, chart.index, { kind: "epsilon" });
    }
  }

  private completeWithExistingNullable(
    chart: Chart,
    waiter: EarleyItem,
    symbol: string,
    completionQueue: EarleyItem[],
    scheduledCompletions: Set<number>,
  ): void {
    for (const completed of chart.items) {
      const isLocalCompletion =
        completed.start === chart.index &&
        completed.end === chart.index &&
        completed.dot === completed.rule.rhs.length &&
        completed.rule.lhs === symbol;
      if (!isLocalCompletion) {
        continue;
      }
      const { item: advanced, linkAdded } = this.addItem(chart, waiter.rule, waiter.dot + 1, waiter.start, {
        kind: "complete",
        waiterItemId: waiter.id,
        completedItemId: completed.id,
      });
      if (
        linkAdded &&
        advanced.dot === advanced.rule.rhs.length &&
        !scheduledCompletions.has(advanced.id)
      ) {
        scheduledCompletions.add(advanced.id);
        completionQueue.push(advanced);
      }
    }
  }

  private scan(
    charts: Chart[],
    chartIndex: number,
    item: EarleyItem,
    symbol: string,
    tokens: readonly InputToken[],
  ): void {
    const token = tokens[chartIndex];
    if (token === undefined || token.kind !== symbol) {
      return;
    }

    this.addItem(
      charts[chartIndex + 1]!,
      item.rule,
      item.dot + 1,
      item.start,
      { kind: "scan", waiterItemId: item.id, tokenIndex: chartIndex },
    );
  }

  private complete(
    charts: Chart[],
    chart: Chart,
    completed: EarleyItem,
    completionQueue: EarleyItem[],
    scheduledCompletions: Set<number>,
  ): void {
    const origin = charts[completed.start]!;
    for (const waiter of origin.items) {
      if (waiter.end !== completed.start) {
        continue;
      }
      const expected = waiter.rule.rhs[waiter.dot];
      if (expected !== completed.rule.lhs) {
        continue;
      }
      const { item: advanced, linkAdded } = this.addItem(chart, waiter.rule, waiter.dot + 1, waiter.start, {
        kind: "complete",
        waiterItemId: waiter.id,
        completedItemId: completed.id,
      });
      if (
        linkAdded &&
        advanced.dot === advanced.rule.rhs.length &&
        !scheduledCompletions.has(advanced.id)
      ) {
        scheduledCompletions.add(advanced.id);
        completionQueue.push(advanced);
      }
    }
  }

  private processCompletion(
    charts: Chart[],
    chart: Chart,
    completed: EarleyItem,
    completionQueue: EarleyItem[],
    scheduledCompletions: Set<number>,
  ): void {
    this.complete(
      charts,
      chart,
      completed,
      completionQueue,
      scheduledCompletions,
    );
  }

  private addItem(
    chart: Chart,
    rule: ProductionRule,
    dot: number,
    start: number,
    link: PackedLink,
  ): { item: EarleyItem; linkAdded: boolean } {
    const key = itemKey(rule.id, dot, start);
    const existing = chart.itemByKey.get(key);
    if (existing !== undefined) {
      const linkKey = this.linkKey(link);
      const hadLink = existing.links.some((candidate) => this.linkKey(candidate) === linkKey);
      if (!hadLink) {
        existing.links.push(link);
      }
      return { item: existing, linkAdded: !hadLink };
    }

    const item: EarleyItem = {
      id: this.nextItemId++,
      rule,
      dot,
      start,
      end: chart.index,
      links: [link],
    };
    chart.itemByKey.set(key, item);
    chart.items.push(item);
    return { item, linkAdded: true };
  }

  private linkKey(link: PackedLink): string {
    switch (link.kind) {
      case "epsilon":
        return "epsilon";
      case "scan":
        return `scan:${link.waiterItemId}:${link.tokenIndex}`;
      case "complete":
        return `complete:${link.waiterItemId}:${link.completedItemId}`;
    }
  }
}
