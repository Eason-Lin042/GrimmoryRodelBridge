// Optional browser verification using the existing reference checkout's Playwright.
// All requests are intercepted with synthetic data; no services or credentials are used.
const { chromium, expect } = require("../ref/grimmory/frontend/node_modules/@playwright/test");
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");

async function main() {
    const root = path.join(__dirname, "..");
    const output = path.join(root, ".local", "bilingual-ui");
    await fs.mkdir(output, { recursive: true });
    const resources = {};
    for (const file of ["index.html", "app.js", "i18n.mjs", "style.css"]) {
        resources[file] = await fs.readFile(path.join(root, "internal", "adapter", "web", file), "utf8");
    }
    const browser = await chromium.launch({ channel: "chrome", headless: true });
    const errors = [];
    const account = {
        username: "synthetic-reader",
        canDownload: false,
        readerURL: "https://bridge.example",
        opdsURL: "https://bridge.example/opds",
        contract: "This deliberately differs from the localized UI contract.",
        books: [
            { bookId: 42, state: "bound", code: "", lastSuccess: 1792310084000 },
            { bookId: 43, state: "paused", code: "HISTORICAL_PROGRESS_UNBOUND", lastSuccess: 0 },
            { bookId: 44, state: "pending", code: "WRITE_READBACK_PENDING", lastSuccess: 0 },
            { bookId: 45, state: "new", code: "", lastSuccess: 0 },
            { bookId: 46, state: "future-state", code: "FUTURE_BACKEND_ERROR", lastSuccess: 0 },
        ],
    };
    let authorized = false;
    let rejectLogin = true;
    let failLogout = false;
    let statusMode = "ok";
    const context = await browser.newContext({ locale: "en-AU", viewport: { width: 1280, height: 900 }, colorScheme: "light" });
    const fulfillJSON = (route, status, body) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
    const serveAssets = async route => {
        const name = new URL(route.request().url()).pathname.slice(1) || "index.html";
        if (name === "favicon.ico") return route.fulfill({ status: 204 });
        if (!(name in resources)) return route.fulfill({ status: 404 });
        return route.fulfill({
            status: 200,
            contentType: name.endsWith("html") ? "text/html" : name.endsWith("css") ? "text/css" : "text/javascript",
            headers: { "Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'" },
            body: resources[name],
        });
    };
    await context.route("http://bridge.test/**", async route => {
        const pathname = new URL(route.request().url()).pathname;
        if (pathname === "/api/status") {
            if (statusMode === "network") return route.abort();
            if (statusMode === "503") return fulfillJSON(route, 503, { error: "UPSTREAM_OR_STORAGE_UNAVAILABLE" });
            if (!authorized || statusMode === "401") return fulfillJSON(route, 401, { error: "NEED_LOGIN" });
            return fulfillJSON(route, 200, account);
        }
        if (pathname === "/reader3/login") {
            if (rejectLogin) return fulfillJSON(route, 401, { isSuccess: false, errorMsg: "NEED_LOGIN", data: null });
            authorized = true;
            return fulfillJSON(route, 200, { isSuccess: true, data: { accessToken: "a".repeat(64) } });
        }
        if (pathname === "/reader3/logout") {
            if (failLogout) return fulfillJSON(route, 503, { isSuccess: false, errorMsg: "UPSTREAM_OR_STORAGE_UNAVAILABLE" });
            authorized = false;
            return fulfillJSON(route, 200, { isSuccess: true, data: "" });
        }
        return serveAssets(route);
    });
    const page = await context.newPage();
    page.on("pageerror", error => errors.push(error.message));
    const lang = value => page.locator(`[data-language="${value}"]`).click();
    const assertTranslated = async () => {
        assert(await page.evaluate(() => [...document.querySelectorAll("[data-i18n]")].every(element => element.textContent.trim())));
        assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), "page has horizontal overflow");
    };
    try {
        await page.goto("http://bridge.test/");
        await expect(page.locator("html")).toHaveAttribute("lang", "en");
        await expect(page.locator("#login")).toBeVisible();
        await page.screenshot({ path: path.join(output, "login-en.png"), fullPage: true });
        await page.locator('[name="username"]').fill("synthetic-reader");
        await page.locator('[name="password"]').fill("synthetic-password");
        await lang("zh-CN");
        await expect(page.locator('[name="username"]')).toHaveValue("synthetic-reader");
        await expect(page.locator('[name="password"]')).toHaveValue("synthetic-password");
        await expect(page.locator("#login-submit")).toHaveText("登录并生成此设备令牌");
        await page.locator("#login-submit").click();
        await expect(page.locator("#message")).toContainText("Grimmory 用户名或密码未通过验证");
        await expect(page.locator('[name="password"]')).toHaveValue("");
        await lang("en");
        await expect(page.locator("#message")).toContainText("username or password was not accepted");
        rejectLogin = false;
        await page.locator('[name="password"]').fill("synthetic-password");
        await page.locator("#login-submit").click();
        await expect(page.locator("#account")).toBeVisible();
        await expect(page.locator("#token")).toHaveText("a".repeat(64));
        await expect(page.locator("#books .state")).toHaveText(["Bound", "Sync paused", "Waiting for write confirmation", "Waiting for the first save", "Unknown sync status"]);
        await expect(page.locator("#books")).toContainText("unrecognized error");
        await expect(page.locator("#download-permission")).toContainText("cannot download");
        await expect(page.locator("#contract")).toContainText("same content version");
        await assertTranslated();
        await page.screenshot({ path: path.join(output, "status-en.png"), fullPage: true, mask: [page.locator("#token")] });
        await lang("zh-CN");
        await expect(page.locator("#books .state")).toHaveText(["已绑定", "同步暂停", "等待写回确认", "等待首次保存", "同步状态未知"]);
        await expect(page.locator("#books")).toContainText("未识别的错误");
        await expect(page.locator("#download-permission")).toContainText("没有下载权限");
        await expect(page.locator("#token")).toHaveText("a".repeat(64));
        const storage = await page.evaluate(() => ({ ...localStorage }));
        assert.deepEqual(storage, { "grimmory-rodel-bridge.language": "zh-CN" });
        await page.screenshot({ path: path.join(output, "status-zh.png"), fullPage: true, mask: [page.locator("#token")] });
        for (const language of ["en", "zh-CN"]) {
            await lang(language);
            const expected = await page.evaluate(locale => new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "medium" }).format(1792310084000), language);
            await expect(page.locator(".last-success").first()).toContainText(expected);
            await page.setViewportSize({ width: 390, height: 844 });
            await assertTranslated();
            await page.screenshot({ path: path.join(output, `mobile-${language}.png`), fullPage: true, mask: [page.locator("#token")] });
        }
        await page.setViewportSize({ width: 1280, height: 900 });
        await page.emulateMedia({ colorScheme: "dark" });
        await page.screenshot({ path: path.join(output, "status-dark.png"), fullPage: true, mask: [page.locator("#token")] });
        statusMode = "503";
        await page.locator("#refresh").click();
        await expect(page.locator("#message")).toContainText("状态刷新失败");
        await expect(page.locator("#account")).toBeVisible();
        await lang("en");
        await expect(page.locator("#message")).toContainText("Could not refresh status");
        statusMode = "network";
        await page.locator("#refresh").click();
        await expect(page.locator("#message")).toContainText("Cannot reach the service");
        statusMode = "ok";
        failLogout = true;
        await page.locator("#logout").click();
        await expect(page.locator("#message")).toContainText("Could not revoke");
        await expect(page.locator("#token-panel")).toBeVisible();
        await page.reload();
        await expect(page.locator("html")).toHaveAttribute("lang", "en");
        await expect(page.locator("#account")).toBeVisible();
        await expect(page.locator("#token-panel")).toBeHidden();
        statusMode = "401";
        await page.locator("#refresh").click();
        await expect(page.locator("#login")).toBeVisible();
        await expect(page.locator("#message")).toContainText("session is no longer valid");
        statusMode = "ok";
        account.books = [];
        await page.reload();
        await expect(page.locator("#books")).toContainText("No books have been prepared");
        failLogout = false;
        await page.locator("#logout").click();
        await expect(page.locator("#login")).toBeVisible();
        await expect(page.locator("#message")).toContainText("has been revoked");
        await lang("zh-CN");
        await expect(page.locator("#message")).toContainText("令牌已撤销");
        const privateContext = await browser.newContext({ locale: "zh-TW", viewport: { width: 390, height: 844 } });
        await privateContext.addInitScript(() => Object.defineProperty(window, "localStorage", { get() { throw new DOMException("Storage disabled", "SecurityError"); } }));
        await privateContext.route("http://bridge.test/**", async route => {
            if (new URL(route.request().url()).pathname === "/api/status") return fulfillJSON(route, 401, { error: "NEED_LOGIN" });
            return serveAssets(route);
        });
        const privatePage = await privateContext.newPage();
        privatePage.on("pageerror", error => errors.push(error.message));
        await privatePage.goto("http://bridge.test/");
        await expect(privatePage.locator("html")).toHaveAttribute("lang", "zh-CN");
        await privatePage.locator('[data-language="en"]').click();
        await expect(privatePage.locator("#login-submit")).toHaveText("Sign in and create a device token");
        await privateContext.close();
        assert.deepEqual(errors, [], "browser JavaScript errors");
        console.log("Bilingual browser checks passed: language detection/persistence, login, status/error translation, dates, token privacy, storage-disabled mode, desktop/mobile and dark mode.");
        console.log("Screenshots:", output);
    } catch (error) {
        await page.screenshot({ path: path.join(output, "failure.png"), fullPage: true, mask: [page.locator("#token")] });
        throw error;
    } finally {
        await browser.close();
    }
}

main().catch(error => { console.error(error); process.exitCode = 1; });
