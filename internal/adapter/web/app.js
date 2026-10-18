import { errorMessage, formatTime, languageStorageKey, selectLocale, states, translate } from "./i18n.mjs";

const $ = id => document.getElementById(id);
let savedLanguage;
try { savedLanguage = localStorage.getItem(languageStorageKey); } catch { /* Storage may be disabled. */ }
let language = selectLocale(savedLanguage, navigator.languages || [navigator.language]);
const ui = { account: null, token: "", notice: null, busy: null };
const t = (key, values) => translate(key, language, values);

function clearSession() {
    ui.account = null;
    ui.token = "";
}

function appendErrorCode(parent, code) {
    if (!code) return;
    const line = document.createElement("p");
    line.className = "error-code muted";
    const value = document.createElement("code");
    value.textContent = code;
    line.append(`${t("errorCode")}: `, value);
    parent.append(line);
}

function renderBooks() {
    $("books").replaceChildren();
    const books = ui.account.books || [];
    if (!books.length) {
        const empty = document.createElement("p");
        empty.className = "empty-state muted";
        empty.textContent = t("emptyBooks");
        $("books").append(empty);
    }
    for (const book of books) {
        const item = document.createElement("article");
        item.className = "book-status";
        const heading = document.createElement("h3");
        heading.textContent = t("bookHeading", { id: book.bookId });
        const state = document.createElement("span");
        state.className = "state";
        state.dataset.state = states[book.state] ? book.state : "unknown";
        state.textContent = t(states[book.state] || "stateUnknown");
        const reason = document.createElement("p");
        reason.textContent = book.code ? errorMessage(book.code, language) : t("noErrors");
        const last = document.createElement("p");
        last.className = "muted last-success";
        last.textContent = t("lastSuccess", { time: formatTime(book.lastSuccess, language) });
        item.append(heading, state, reason, last);
        appendErrorCode(item, book.code);
        $("books").append(item);
    }
}

function render() {
    document.documentElement.lang = language;
    for (const element of document.querySelectorAll("[data-i18n]")) element.textContent = t(element.dataset.i18n);
    for (const element of document.querySelectorAll("[data-i18n-label]")) element.setAttribute("aria-label", t(element.dataset.i18nLabel));
    for (const button of document.querySelectorAll("[data-language]")) button.setAttribute("aria-pressed", String(button.dataset.language === language));
    for (const input of $("login").querySelectorAll("input")) {
        if (input.validity.customError) input.setCustomValidity(t(`${input.name}Required`));
    }
    $("login").hidden = !!ui.account;
    $("account").hidden = !ui.account;
    $("token-panel").hidden = !ui.token;
    $("token").textContent = ui.token;
    $("login-submit").disabled = !!ui.busy;
    $("refresh").disabled = !!ui.busy;
    $("logout").disabled = !!ui.busy;
    $("login").setAttribute("aria-busy", String(ui.busy === "login"));
    $("account").setAttribute("aria-busy", String(ui.busy === "refresh" || ui.busy === "logout"));
    if (ui.busy === "login") $("login-submit").textContent = t("signingIn");
    if (ui.busy === "refresh") $("refresh").textContent = t("refreshing");
    if (ui.busy === "logout") $("logout").textContent = t("revoking");
    $("message").replaceChildren();
    $("message").hidden = !ui.notice;
    if (ui.notice) {
        const text = document.createElement("span");
        text.textContent = t(ui.notice.key, { reason: errorMessage(ui.notice.code, language, ui.notice.context) });
        $("message").append(text);
        appendErrorCode($("message"), ui.notice.code);
    }
    if (ui.account) {
        $("username").textContent = ui.account.username;
        $("reader-url").textContent = ui.account.readerURL;
        $("opds-url").textContent = ui.account.opdsURL;
        $("download-permission").textContent = t(ui.account.canDownload ? "downloadAllowed" : "downloadDenied");
        renderBooks();
    }
}

function responseCode(body, response) {
    // Never display arbitrary upstream response text as an error message.
    const value = body?.errorMsg || body?.error;
    return typeof value === "string" && /^[A-Z][A-Z0-9_]{0,79}$/.test(value) ? value : `HTTP_${response.status}`;
}

async function responseBody(response) {
    try { return await response.json(); } catch { throw new Error("UPSTREAM_OR_STORAGE_UNAVAILABLE"); }
}

function showFailure(key, error, context) {
    const code = /^[A-Z][A-Z0-9_]{0,79}$/.test(error.message) ? error.message : null;
    ui.notice = code ? { key, code, context } : { key: "connectionFailed" };
}

async function refreshStatus(initial = false) {
    const response = await fetch("/api/status", { cache: "no-store" });
    if (response.status === 401) {
        clearSession();
        if (!initial) ui.notice = { key: "sessionExpired" };
        render();
        return;
    }
    const data = await responseBody(response);
    if (!response.ok) throw new Error(responseCode(data, response));
    ui.account = data;
    render();
}

for (const button of document.querySelectorAll("[data-language]")) {
    button.addEventListener("click", () => {
        language = button.dataset.language;
        try { localStorage.setItem(languageStorageKey, language); } catch { /* Switching still works without storage. */ }
        render();
    });
}

for (const input of $("login").querySelectorAll("input")) {
    input.addEventListener("invalid", () => input.setCustomValidity(t(`${input.name}Required`)));
    input.addEventListener("input", () => input.setCustomValidity(""));
}

$("login").addEventListener("submit", async event => {
    event.preventDefault();
    ui.busy = "login";
    ui.notice = null;
    render();
    const form = new FormData(event.target);
    try {
        const response = await fetch("/reader3/login", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ username: form.get("username"), password: form.get("password"), isLogin: true }),
        });
        const data = await responseBody(response);
        if (!response.ok || !data.isSuccess) throw new Error(responseCode(data, response));
        ui.token = data.data.accessToken;
        ui.notice = { key: "tokenCreated" };
        // Status failures must not be misreported as a rejected login.
        try { await refreshStatus(); } catch (error) { showFailure("statusFailed", error); }
    } catch (error) {
        showFailure("loginFailed", error, "login");
    } finally {
        event.target.elements.password.value = "";
        ui.busy = null;
        render();
    }
});

$("refresh").addEventListener("click", async () => {
    ui.busy = "refresh";
    ui.notice = null;
    render();
    try { await refreshStatus(); } catch (error) { showFailure("statusFailed", error); }
    finally { ui.busy = null; render(); }
});

$("logout").addEventListener("click", async () => {
    ui.busy = "logout";
    ui.notice = null;
    render();
    try {
        const response = await fetch("/reader3/logout", { method: "POST" });
        if (response.status === 401) {
            clearSession();
            ui.notice = { key: "sessionExpired" };
        } else {
            const data = await responseBody(response);
            if (!response.ok || !data.isSuccess) throw new Error(responseCode(data, response));
            clearSession();
            ui.notice = { key: "loggedOut" };
        }
    } catch (error) { showFailure("logoutFailed", error); }
    finally { ui.busy = null; render(); }
});

render();
refreshStatus(true).catch(error => { showFailure("statusFailed", error); render(); });
