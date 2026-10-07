/**
 * Shared Packed Parse Forest (SPPF).
 *
 * Node kinds
 * ----------
 * - symbol node: (symbol, i, j) — a parse of `symbol` spanning positions i..j.
 * - intermediate node: (ruleIndex, dot, i, j) — a parse of the prefix
 *   rhs[0:dot] of a rule, spanning i..j. dot === rhs.length means the whole RHS.
 * - terminal node: (terminal, j-1, j) for a scanned token.
 *
 * Every node keeps a list of "families". A symbol node normally has one family
 * per completed rule (left: full intermediate, right: null); an intermediate
 * node can have several families (left: symbol/intermediate child for rhs[k],
 * right: intermediate for rhs[0:k]), which is exactly where ambiguity lives.
 *
 * Families are de-duplicated by child identity so repeated derivations caused
 * by the same parse state never collapse genuinely distinct parses, while the
 * forest stays shared for identical sub-derivations.
 */

export type NodeKind = "symbol" | "intermediate" | "terminal" | "empty";

export interface Family {
  readonly left: number | null;
  readonly right: number | null;
  /** Origin label for diagnostics: which rule/split created this family. */
  readonly origin: string;
}

export interface SppfNode {
  readonly id: number;
  readonly kind: NodeKind;
  /** Symbol name (symbol/terminal) or rule index label (intermediate). */
  readonly label: string;
  readonly start: number;
  readonly end: number;
  /** Intermediate nodes only: dot position into the rule RHS. */
  readonly dot?: number;
  /** Intermediate nodes only: source rule index. */
  readonly ruleIndex?: number;
  families: Family[];
  /** Family-key set used for de-duplication. */
  readonly familyKeys: Set<string>;
}

let EMPTY_ID = -1;

export class SppfBuilder {
  readonly nodes: SppfNode[] = [];
  private readonly symbolIndex = new Map<string, number>();
  private readonly intermediateIndex = new Map<string, number>();
  private readonly terminalIndex = new Map<string, number>();
  private readonly emptyShared: number;

  constructor() {
    // Single shared epsilon node at (0,0); spans are attached via family
    // positions so one node represents every empty completion location.
    this.emptyShared = this.freshNode({
      kind: "empty",
      label: "epsilon",
      start: 0,
      end: 0,
    });
  }

  emptyNode(): number {
    return this.emptyShared;
  }

  private freshNode(init: Omit<SppfNode, "id" | "families" | "familyKeys">): number {
    const id = this.nodes.length;
    this.nodes.push({ ...init, id, families: [], familyKeys: new Set() });
    return id;
  }

  getSymbol(label: string, start: number, end: number): number {
    const key = `${label}:${start}:${end}`;
    const existing = this.symbolIndex.get(key);
    if (existing !== undefined) return existing;
    const id = this.freshNode({ kind: "symbol", label, start, end });
    this.symbolIndex.set(key, id);
    return id;
  }

  getTerminal(label: string, start: number, end: number): number {
    const key = `${label}:${start}:${end}`;
    const existing = this.terminalIndex.get(key);
    if (existing !== undefined) return existing;
    const id = this.freshNode({ kind: "terminal", label, start, end });
    this.terminalIndex.set(key, id);
    return id;
  }

  getIntermediate(ruleIndex: number, dot: number, start: number, end: number): number {
    const key = `${ruleIndex}:${dot}:${start}:${end}`;
    const existing = this.intermediateIndex.get(key);
    if (existing !== undefined) return existing;
    const id = this.freshNode({
      kind: "intermediate",
      label: `rule${ruleIndex}•${dot}`,
      start,
      end,
      dot,
      ruleIndex,
    });
    this.intermediateIndex.set(key, id);
    return id;
  }

  /** Attach a family if not already present. Returns true when newly added. */
  addFamily(nodeId: number, left: number | null, right: number | null, origin: string): boolean {
    const node = this.nodes[nodeId]!;
    const key = `${left === null ? "_" : left}/${right === null ? "_" : right}`;
    if (node.familyKeys.has(key)) return false;
    node.familyKeys.add(key);
    node.families.push({ left, right, origin });
    return true;
  }

  get(id: number): SppfNode {
    return this.nodes[id]!;
  }
}
