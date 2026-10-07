import { readFile } from "node:fs/promises";

export interface EarleyConfig {
  recognition: {
    includeChartSnapshots: boolean;
    maxDiagnosticItemsPerChart: number;
  };
  forest: {
    maxDerivations: number;
    maxExpansions: number;
  };
  diagnostics: {
    redactInput: boolean;
    showTerminalValues: boolean;
    mask: string;
  };
}

export const defaultConfig: EarleyConfig = {
  recognition: {
    includeChartSnapshots: true,
    maxDiagnosticItemsPerChart: 12,
  },
  forest: {
    maxDerivations: 1000,
    maxExpansions: 5000,
  },
  diagnostics: {
    redactInput: true,
    showTerminalValues: false,
    mask: "***redacted***",
  },
};

export async function loadConfig(path: string): Promise<EarleyConfig> {
  const raw = JSON.parse(await readFile(path, "utf8")) as Partial<EarleyConfig>;
  return {
    recognition: { ...defaultConfig.recognition, ...raw.recognition },
    forest: { ...defaultConfig.forest, ...raw.forest },
    diagnostics: { ...defaultConfig.diagnostics, ...raw.diagnostics },
  };
}
