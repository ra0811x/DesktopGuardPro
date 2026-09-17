import { readFile } from "node:fs/promises";

import { describe, expect, it } from "vitest";

import configExport from "./vite.config";

async function resolvedConfig() {
  if (typeof configExport === "function") {
    return await configExport({ command: "serve", mode: "test", isSsrBuild: false, isPreview: false });
  }
  return await configExport;
}

describe("development content security policy", () => {
  it("allows Vite style injection only through the serve plugin", async () => {
    const config = await resolvedConfig();
    const plugins = (config.plugins ?? []).flat(Infinity).filter(Boolean) as Array<{
      name?: string;
      apply?: string;
      transformIndexHtml?: ((html: string) => unknown) | { handler: (html: string) => unknown };
    }>;
    const plugin = plugins.find((candidate) => candidate.name === "desktop-guard-development-csp");

    expect(plugin?.apply).toBe("serve");
    const hook = plugin?.transformIndexHtml;
    const transform = typeof hook === "function" ? hook : hook?.handler;
    expect(transform).toBeTypeOf("function");

    const source = await readFile(new URL("./index.html", import.meta.url), "utf8");
    const transformed = await transform!(source) as string;
    expect(transformed).toContain("style-src 'self' 'unsafe-inline'");
    expect(source).toContain("style-src 'self';");
    expect(source).not.toContain("'unsafe-inline'");
  });
});
