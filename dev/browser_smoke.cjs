// Opt-in real Web UI test. Requires the local upstream build and Chrome.
// Reads only generated, isolated test credentials; never prints tokens/passwords.
const { chromium, expect } = require("../ref/grimmory/frontend/node_modules/@playwright/test");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const data = path.join(__dirname, "../.local/grimmory");

async function main() {
  const account = JSON.parse(fs.readFileSync(path.join(data, "client-test.json"), "utf8"));
  const browser = await chromium.launch({ channel: "chrome", headless: true });
  const context = await browser.newContext({ locale: "zh-CN", viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  try {
    // Complete the official password-change flow for this generated test account,
    // including credentials created by an earlier version of the test harness.
    assert(account.username.startsWith("adapter_reader_"), "Expected an isolated test account");
    const initial = await context.request.post("http://127.0.0.1:6060/api/v1/auth/login", { data: { username: account.username, password: account.password } });
    const initialJWT = (await initial.json()).accessToken;
    const changedPassword = require("node:crypto").randomBytes(24).toString("base64url");
    const changed = await context.request.put("http://127.0.0.1:6060/api/v1/users/change-password", {
      headers: { Authorization: "Bearer " + initialJWT }, data: { currentPassword: account.password, newPassword: changedPassword }
    });
    assert.equal(changed.status(), 200, "Test-account password change failed");
    account.password = changedPassword;
    fs.writeFileSync(path.join(data, "client-test.json"), JSON.stringify(account, null, 2));
    await page.goto("http://127.0.0.1:8080");
    await page.locator('input[name="username"]').fill(account.username);
    await page.locator('input[name="password"]').fill(account.password);
    await page.getByRole("button", { name: "登录并生成此设备令牌" }).click();
    await expect(page.locator("#account")).toBeVisible();
    await expect(page.locator("#username")).toHaveText(account.username);
    await expect(page.locator("#token")).toHaveText(/^[a-f0-9]{64}$/);
    account.adapterToken = await page.locator("#token").textContent();
    await page.screenshot({ path: path.join(data, "adapter-config.png"), fullPage: true, mask: [page.locator("#token")] });
    console.log("Adapter configuration page login and bound status: passed");
    const readerQuery = "?accessToken=" + encodeURIComponent(account.adapterToken);
    const save = async index => {
      const response = await context.request.post("http://127.0.0.1:8080/reader3/saveBookProgress" + readerQuery,
        { data: { url: account.syncURL, index } });
      assert.equal(response.status(), 200, "Reader save failed");
    };
    const readerBook = async () => {
      const response = await context.request.get("http://127.0.0.1:8080/reader3/getShelfBook" + readerQuery + "&url=" + encodeURIComponent(account.syncURL));
      const body = await response.json();
      assert.equal(response.status(), 200, "Reader restore failed: " + (body.errorMsg || ""));
      return body.data;
    };
    await save(1);
    await page.waitForTimeout(1100); // Upstream timestamps have second precision.
    await page.goto("http://127.0.0.1:6060/login");
    await page.locator('input[name="username"]').fill(account.username);
    await page.locator('input[type="password"]').fill(account.password);
    const loginResponse = page.waitForResponse(r => r.url().endsWith("/api/v1/auth/login") && r.request().method() === "POST");
    await page.locator('button[type="submit"]').click();
    const jwt = (await (await loginResponse).json()).accessToken;
    assert(jwt, "Web login did not return a token");
    await page.waitForURL(url => !url.pathname.includes("login"));
    await page.goto(`http://127.0.0.1:6060/ebook-reader/book/${account.bookId}`);
    await page.locator("foliate-view").waitFor({ timeout: 60000 });
    await expect(page.locator(".loader-overlay")).toBeHidden({ timeout: 60000 });
    // Read Foliate's public relocation result; navigation is exercised via the UI.
    const currentChapter = () => page.evaluate(() => document.querySelector("foliate-view")?.lastLocation?.tocItem?.label || "");
    await page.screenshot({ path: path.join(data, "web-reader-restored.png") });
    await expect.poll(currentChapter, { timeout: 15000 }).toBe("C2 Green");
    await page.keyboard.press("t");
    await page.locator(".chapter-row").filter({ hasText: "C3 Purple" }).click();
    await expect.poll(currentChapter, { timeout: 15000 }).toBe("C3 Purple");
    // Wait for the application's own debounced save to reach the real backend.
    await expect.poll(async () => {
      const response = await context.request.get(`http://127.0.0.1:6060/api/v1/books/${account.bookId}`,
        { headers: { Authorization: "Bearer " + jwt } });
      const book = await response.json();
      return book.epubProgress?.href || "";
    }, { timeout: 15000 }).toContain("b.xhtml");
    const restored = await readerBook();
    assert.equal(restored.durChapterIndex, 2, "Actual Web save did not map to Reader chapter 2");
    assert(restored.durChapterTime > 0);
    await page.screenshot({ path: path.join(data, "web-reader-chapter3.png") });
    await page.goto("http://127.0.0.1:6060/all-books");
    await save(0);
    await page.waitForTimeout(1100);
    await page.goto(`http://127.0.0.1:6060/ebook-reader/book/${account.bookId}`);
    await expect.poll(currentChapter, { timeout: 60000 }).toBe("C1 Amber");
    console.log("Actual Grimmory Web href restore -> UI chapter change -> real CFI save -> Reader restore -> Web restore: passed");
    fs.writeFileSync(path.join(data, "browser-test-result.json"), JSON.stringify({ bookId: account.bookId, checkedAt: new Date().toISOString(), result: "real Web UI + Reader API passed; native Rodel not automated" }, null, 2));
  } catch (error) {
    await page.screenshot({ path: path.join(data, "browser-failure.png"), mask: [page.locator("#token")] }).catch(() => {});
    throw error;
  } finally {
    await browser.close();
  }
}

main().catch(error => { console.error(error.message); process.exitCode = 1; });
