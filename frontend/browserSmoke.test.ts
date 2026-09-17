import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

import { chromium, type Browser } from "playwright-core";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { createServer, type ViteDevServer } from "vite";

const edgeExecutable = [
  "C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe",
  "C:/Program Files/Microsoft/Edge/Application/msedge.exe",
].find(existsSync);

const browserSuite = edgeExecutable ? describe : describe.skip;

browserSuite("development browser smoke test", () => {
  let server: ViteDevServer;
  let browser: Browser;

  beforeAll(async () => {
    server = await createServer({
      root: process.cwd(),
      configFile: resolve(process.cwd(), "vite.config.ts"),
      logLevel: "silent",
      server: { host: "127.0.0.1", port: 0, strictPort: false },
    });
    await server.listen();
    browser = await chromium.launch({ headless: true, executablePath: edgeExecutable });
  }, 30_000);

  afterAll(async () => {
    await browser?.close();
    await server?.close();
  });

  it("declares the browser driver in the project dependency lock", () => {
    const packageJSON = JSON.parse(readFileSync(resolve(process.cwd(), "package.json"), "utf8"));

    expect(packageJSON.devDependencies?.["playwright-core"]).toBe("1.63.0");
  });

  it("loads styles and keeps the development CSP free of style violations", async () => {
    const url = server.resolvedUrls?.local[0];
    if (!url) throw new Error("Vite did not expose a local development URL");
    const page = await browser.newPage();
    const cspErrors: string[] = [];
    page.on("console", (message) => {
      if (message.type() === "error" && message.text().includes("Content Security Policy")) {
        cspErrors.push(message.text());
      }
    });

    await page.goto(url, { waitUntil: "networkidle" });
    const result = await page.evaluate(() => ({
      styleSheetCount: document.styleSheets.length,
      bodyBackground: getComputedStyle(document.body).backgroundColor,
      appDisplay: getComputedStyle(document.querySelector<HTMLElement>(".app-shell")!).display,
      currentPage: document.querySelector(".nav-item[aria-current='page']")?.textContent,
      contentSecurityPolicy: document.querySelector("meta[http-equiv='Content-Security-Policy']")?.getAttribute("content"),
    }));
    await page.close();

    expect(cspErrors).toEqual([]);
    expect(result.styleSheetCount).toBeGreaterThan(0);
    expect(result.bodyBackground).toBe("rgb(243, 247, 252)");
    expect(result.appDisplay).toBe("grid");
    expect(result.currentPage).toBe("仪表盘");
    expect(result.contentSecurityPolicy).toContain("style-src 'self' 'unsafe-inline'");
  }, 30_000);
});
