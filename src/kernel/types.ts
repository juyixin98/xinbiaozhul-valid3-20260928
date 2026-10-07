/** Outcome classification shared by the kernel, diagnostics and CLI. */
export type RecognitionVerdict = "accept" | "reject";

export type RejectReason =
  | "NO_PARSE" // recognizer closed without a completed start item
  | "UNPRODUCTIVE_GRAMMAR"; // no derivation of any terminal string can exist

export interface ParseResult {
  verdict: RecognitionVerdict;
  /** Present exactly when verdict === "accept". */
  rootNodeId: number | null;
  inputLength: number;
  /** Number of chart sets built (inputLength + 1). */
  setCount: number;
  itemCount: number;
  rejectReason: RejectReason | null;
}
