import { chromium } from "@playwright/test";

const browser = await chromium.launch({ headless: true });
const page = await browser.newPage();
await page.goto("https://example.com", { waitUntil: "domcontentloaded" });
const title = await page.title();
if (!title.toLowerCase().includes("example")) {
  throw new Error(`unexpected title: ${title}`);
}
await browser.close();
console.log("pw:smoke: OK");
