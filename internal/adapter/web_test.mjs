import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { errorKeys, errorMessage, formatTime, locales, messages, selectLocale, states, translate } from "./web/i18n.mjs";

test("both languages cover every message and use matching placeholders", () => {
    for (const [key, entry] of Object.entries(messages)) {
        assert.deepEqual(Object.keys(entry).sort(), [...locales].sort(), key);
        const placeholders = text => [...text.matchAll(/\{(\w+)\}/g)].map(match => match[1]).sort();
        assert.deepEqual(placeholders(entry.en), placeholders(entry["zh-CN"]), key);
        for (const locale of locales) assert(entry[locale].trim(), `${key}: ${locale}`);
        assert.doesNotMatch(entry.en, /\p{Script=Han}/u, `English message contains untranslated Chinese: ${key}`);
    }
});

test("saved preference wins, regional Chinese works, unsupported languages fall back", () => {
    assert.equal(selectLocale("en", ["zh-CN"]), "en");
    assert.equal(selectLocale("zh-CN", ["en-AU"]), "zh-CN");
    assert.equal(selectLocale(null, ["zh-TW", "en"]), "zh-CN");
    assert.equal(selectLocale("invalid", ["fr-FR", "en-AU"]), "en");
    assert.equal(selectLocale(null, ["de"]), "en");
});

test("static page keys and dynamic status/error keys have complete translations", async () => {
    const html = await readFile(new URL("./web/index.html", import.meta.url), "utf8");
    const keys = [...html.matchAll(/data-i18n(?:-label)?="([^"]+)"/g)].map(match => match[1]);
    for (const key of [...keys, ...Object.values(states), ...Object.values(errorKeys)]) {
        for (const locale of locales) assert(translate(key, locale));
    }
    assert.match(errorMessage("PROGRESS_TIME_AMBIGUOUS", "en"), /same-second/);
    assert.match(errorMessage("NEED_LOGIN", "en", "login"), /username or password/);
    assert.match(errorMessage("NEED_LOGIN", "zh-CN"), /会话/);
    assert.match(errorMessage("UPSTREAM_PROGRESS_AVAILABLE", "en"), /load it before saving/);
    assert.match(errorMessage("UPSTREAM_PROGRESS_AVAILABLE", "zh-CN"), /先读取云端位置再保存/);
    assert.equal(errorMessage("FUTURE_BACKEND_ERROR", "en"), messages.errorUnknown.en);
    assert.equal(errorMessage("FUTURE_BACKEND_ERROR", "zh-CN"), messages.errorUnknown["zh-CN"]);
});

test("dates and dynamic values follow the selected language without altering data", () => {
    const timestamp = Date.UTC(2026, 9, 18, 7, 54, 44);
    for (const locale of locales) {
        const expected = new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "medium" }).format(timestamp);
        assert.equal(formatTime(timestamp, locale), expected);
        assert.equal(formatTime(0, locale), translate("noSuccess", locale));
        assert.equal(formatTime("bad timestamp", locale), translate("unavailableTime", locale));
    }
    assert.equal(translate("bookHeading", "en", { id: 42 }), "Book 42");
    assert.equal(translate("bookHeading", "zh-CN", { id: 42 }), "书籍 42");
    assert.equal(translate("lastSuccess", "en", { time: "$& <sample>" }), "Last successful save: $& <sample>");
});
