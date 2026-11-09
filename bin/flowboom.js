#!/usr/bin/env node
import { App } from '../src/cli.js';

const controller = new AbortController();
process.once('SIGINT', () => controller.abort());
process.once('SIGTERM', () => controller.abort());
try {
  await new App({ signal: AbortSignal.any([controller.signal, AbortSignal.timeout(16 * 60 * 1000)]) }).run(process.argv.slice(2));
} catch (error) {
  console.error(controller.signal.aborted ? 'Operation canceled.' : error.message);
  process.exitCode = 1;
}
