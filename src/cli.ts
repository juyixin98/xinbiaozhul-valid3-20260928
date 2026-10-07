#!/usr/bin/env node
import { readFile } from "node:fs/promises";
import { defaultConfig, loadConfig } from "./config.js";
import { recognizeString, redactForLog } from "./parser-service.js";

async function main(): Promise<void> {
  const args = process.argv.slice(2);
  const configPath = args.find((argument) => argument.startsWith("--config="))?.slice("--config=".length);
  const positional = args.filter((argument) => !argument.startsWith("--config="));
  const [grammarPath, input, startSymbol, requestId] = positional;
  if (grammarPath === undefined || input === undefined) {
    console.error("Usage: earley-forest <grammar-file> <input> [start-symbol] [request-id] [--config=path]");
    process.exitCode = 2;
    return;
  }

  const config = configPath === undefined ? defaultConfig : await loadConfig(configPath);
  const grammarSource = await readFile(grammarPath, "utf8");
  const response = recognizeString(
    grammarSource,
    { input, ...(requestId === undefined ? {} : { requestId }) },
    config,
    startSymbol ?? "S",
  );
  console.log(JSON.stringify(redactForLog(response, config), null, 2));
  if (response.diagnostic.outcome !== "accepted") {
    process.exitCode = 1;
  }
}

main().catch((error: unknown) => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
});
