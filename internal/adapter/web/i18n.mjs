export const languageStorageKey = "grimmory-rodel-bridge.language";
export const locales = ["en", "zh-CN"];

// Keep each translation pair together so changes are reviewed in both languages.
export const messages = {
    languageLabel: { en: "Language", "zh-CN": "语言" },
    tagline: { en: "Grimmory ↔ Rodel.Reader · EPUB reading and chapter sync", "zh-CN": "Grimmory ↔ Rodel.Reader · EPUB 阅读与章节同步" },
    loginHeading: { en: "Connect your Grimmory account", "zh-CN": "连接你的 Grimmory 账户" },
    loginHelp: { en: "Use your Grimmory username and password here. Rodel.Reader will use the device token generated after sign-in.", "zh-CN": "在这里使用 Grimmory 用户名和密码登录。小幻使用登录后生成的设备令牌。" },
    usernameLabel: { en: "Username", "zh-CN": "用户名" },
    passwordLabel: { en: "Grimmory password", "zh-CN": "Grimmory 密码" },
    usernameRequired: { en: "Enter your Grimmory username.", "zh-CN": "请输入 Grimmory 用户名。" },
    passwordRequired: { en: "Enter your Grimmory password.", "zh-CN": "请输入 Grimmory 密码。" },
    loginButton: { en: "Sign in and create a device token", "zh-CN": "登录并生成此设备令牌" },
    signingIn: { en: "Signing in…", "zh-CN": "正在登录…" },
    tokenHeading: { en: "This device's access token", "zh-CN": "此设备访问令牌" },
    tokenHelp: { en: "Copy this token into both Rodel.Reader sources. It is shown only after sign-in. Keep it private.", "zh-CN": "请将此令牌复制到小幻的两个源中。令牌仅在登录后显示，请妥善保存，不要分享。" },
    tokenPasswordHelp: { en: "Use this token as the OPDS password, not your Grimmory account password.", "zh-CN": "OPDS 密码填写此令牌，不是 Grimmory 账户密码。" },
    accountHeading: { en: "Connected account", "zh-CN": "已连接账户" },
    contract: { en: "Sync supports one primary EPUB per book, the same content version, and normal updates from Grimmory Web and this bridge. Invalid positions, missing reading times or content changes pause sync.", "zh-CN": "同步仅支持每本书唯一的主 EPUB、同一内容版本，以及 Grimmory Web 和本桥接服务的正常进度更新；位置无效、缺少阅读时间或内容变化时暂停。" },
    readerSource: { en: "Add a Reader source", "zh-CN": "添加 Reader 源" },
    readerHelp: { en: "Set the access token to the device token. Do not append /reader3 to the address.", "zh-CN": "访问令牌填写此设备令牌。地址不需要追加 /reader3。" },
    opdsSource: { en: "Add an OPDS source", "zh-CN": "添加 OPDS 源" },
    opdsHelp: { en: "Use your Grimmory username and the same device token as the password.", "zh-CN": "用户名填写 Grimmory 用户名，密码填写同一个设备令牌。" },
    readingHelp: { en: "Download the EPUB through OPDS, then open it in Rodel.Reader's main library. Keep the Reader source configured for chapter sync.", "zh-CN": "从 OPDS 下载 EPUB，再到小幻主书库打开。保留 Reader 源，用于章节进度同步。" },
    downloadAllowed: { en: "This account can download EPUB files.", "zh-CN": "当前账户可以获取 EPUB。" },
    downloadDenied: { en: "This account cannot download books. Previously downloaded books can still sync while book access remains granted.", "zh-CN": "当前账户没有下载权限；已下载的书籍只要仍有访问权限，就可以继续同步。" },
    versionHelp: { en: "Existing Grimmory progress is adopted automatically when its position and reading time are valid for the current EPUB. After a content update, the old copy stops syncing and a new import may create a second local entry. Existing books are not deleted automatically.", "zh-CN": "Grimmory 已有进度的位置和阅读时间对当前 EPUB 有效时，会自动建立绑定。内容更新后，旧副本停止同步，重新导入可能生成第二个本地条目。已有书籍不会自动删除。" },
    refreshButton: { en: "Refresh status", "zh-CN": "刷新状态" },
    refreshing: { en: "Refreshing…", "zh-CN": "正在刷新…" },
    logoutButton: { en: "Revoke this device token", "zh-CN": "注销此设备令牌" },
    revoking: { en: "Revoking…", "zh-CN": "正在注销…" },
    booksHeading: { en: "Sync status for prepared books", "zh-CN": "已准备书籍的同步状态" },
    statusHelp: { en: "These are the last validation results, not continuous monitoring. Rodel.Reader may keep reading locally when sync fails; check the status and reason here.", "zh-CN": "这里记录最近一次校验结果，不会持续监测。小幻可能在同步失败时继续本地阅读，请在这里查看状态和原因。" },
    emptyBooks: { en: "No books have been prepared yet. Download an EPUB from the OPDS source to see its sync status here.", "zh-CN": "还没有准备好的书籍。从 OPDS 源获取 EPUB 后，可在这里查看同步状态。" },
    bookHeading: { en: "Book {id}", "zh-CN": "书籍 {id}" },
    lastSuccess: { en: "Last successful save: {time}", "zh-CN": "最近成功保存：{time}" },
    noSuccess: { en: "No successful save yet", "zh-CN": "尚无成功保存" },
    unavailableTime: { en: "Time unavailable", "zh-CN": "时间不可用" },
    errorCode: { en: "Error code", "zh-CN": "错误代码" },
    noErrors: { en: "No errors reported.", "zh-CN": "未报告错误。" },
    tokenCreated: { en: "The device token is ready. Copy it into both Rodel.Reader sources.", "zh-CN": "设备令牌已生成，请复制到小幻的两个源中。" },
    loggedOut: { en: "This device token has been revoked.", "zh-CN": "此设备令牌已撤销。" },
    sessionExpired: { en: "Your session is no longer valid. Sign in again to create a device token.", "zh-CN": "当前会话已失效，请重新登录并生成设备令牌。" },
    connectionFailed: { en: "Cannot reach the service. Check the connection and try again.", "zh-CN": "暂时无法连接服务，请检查连接后重试。" },
    loginFailed: { en: "Sign-in failed. {reason}", "zh-CN": "登录失败。{reason}" },
    statusFailed: { en: "Could not refresh status. {reason}", "zh-CN": "状态刷新失败。{reason}" },
    logoutFailed: { en: "Could not revoke the device token. {reason}", "zh-CN": "设备令牌注销失败。{reason}" },
    loginDenied: { en: "The Grimmory username or password was not accepted.", "zh-CN": "Grimmory 用户名或密码未通过验证。" },
    stateNew: { en: "Waiting for the first save", "zh-CN": "等待首次保存" },
    stateBound: { en: "Bound", "zh-CN": "已绑定" },
    statePaused: { en: "Sync paused", "zh-CN": "同步暂停" },
    statePending: { en: "Waiting for write confirmation", "zh-CN": "等待写回确认" },
    stateUnknown: { en: "Unknown sync status", "zh-CN": "同步状态未知" },
    errorUnknown: { en: "The service reported an unrecognized error. Use the error code when reporting this issue.", "zh-CN": "服务返回了未识别的错误，反馈问题时请附上错误代码。" },
    errorSession: { en: "The session is invalid or expired. Sign in again.", "zh-CN": "会话无效或已过期，请重新登录。" },
    errorCredentials: { en: "The request contains conflicting credentials. Use the same device token for both sources.", "zh-CN": "请求中包含相互冲突的凭据，请在两个源中使用同一个设备令牌。" },
    errorLoginInput: { en: "Enter a valid Grimmory username and password.", "zh-CN": "请输入有效的 Grimmory 用户名和密码。" },
    errorRateLimit: { en: "Too many sign-in attempts. Wait before trying again.", "zh-CN": "登录尝试次数过多，请稍后再试。" },
    errorUnavailable: { en: "Grimmory or local storage is unavailable. Try again later.", "zh-CN": "Grimmory 或本地存储暂时不可用，请稍后重试。" },
    errorUpstreamLimit: { en: "Grimmory is limiting requests. Try again later.", "zh-CN": "Grimmory 正在限制请求频率，请稍后重试。" },
    errorAccess: { en: "This account no longer has access to the book.", "zh-CN": "当前账户已无权访问这本书。" },
    errorNotFound: { en: "The book is no longer available in Grimmory.", "zh-CN": "Grimmory 中已找不到这本书。" },
    errorDownload: { en: "The account needs download permission to obtain the EPUB.", "zh-CN": "账户需要下载权限才能获取 EPUB。" },
    errorIdentity: { en: "The upstream user or book identity does not match. Sync has not been accepted.", "zh-CN": "上游用户或书籍身份不匹配，本次同步未被接受。" },
    errorHistorical: { en: "This book was paused under the earlier historical-progress rule. Refresh the Reader source or retry the EPUB download to check whether its Grimmory progress can now be adopted.", "zh-CN": "这本书曾因旧版历史进度规则暂停。请刷新 Reader 源或重新获取 EPUB，检查能否自动采用 Grimmory 已有进度。" },
    errorUpstreamProgress: { en: "Grimmory already has reading progress. Refresh the Reader source or reopen the book to load it before saving. The upstream position has not been changed.", "zh-CN": "Grimmory 已有阅读进度，请刷新 Reader 源或重新打开书籍，先读取云端位置再保存。上游位置未被修改。" },
    errorVersion: { en: "The EPUB version has changed. Obtain the new copy; existing progress is not migrated automatically.", "zh-CN": "EPUB 版本已变化，请重新获取。已有进度不会自动迁移。" },
    errorFiles: { en: "Sync requires a single readable EPUB file. The book's file collection is unsupported or has changed.", "zh-CN": "同步要求书籍只有一份可阅读的 EPUB。当前文件集合不受支持或已变化。" },
    errorSource: { en: "The progress source cannot be established. Existing progress is preserved.", "zh-CN": "无法确定进度来源，已有进度予以保留。" },
    errorReset: { en: "Grimmory cleared the previously bound progress. Sync is paused; it will not restart from zero automatically.", "zh-CN": "Grimmory 清空了此前绑定的进度。同步已暂停，不会自动从零重新绑定。" },
    errorTime: { en: "There is no reliable time for this position. Automatic restore and writes are paused.", "zh-CN": "缺少可归属到此位置的可靠时间，自动恢复和写回已暂停。" },
    errorTimeAmbiguous: { en: "The position changed without a clearly newer time. Sync is paused; same-second updates can cause this.", "zh-CN": "位置已变化，但没有明确更新的时间。同步已暂停，同一秒内的更新也可能触发此状态。" },
    errorUnconfirmed: { en: "The write result is uncertain. Sync is paused to avoid overwriting progress with a retry.", "zh-CN": "写入结果尚不确定。同步已暂停，以免重试覆盖已有进度。" },
    errorReadback: { en: "The saved progress could not be confirmed by reading it back. Sync is paused.", "zh-CN": "无法通过读回确认保存的进度，同步已暂停。" },
    errorInitialRace: { en: "Progress changed during the first binding attempt. The upstream value is preserved and sync is paused.", "zh-CN": "首次绑定期间进度发生变化。上游值予以保留，同步已暂停。" },
    errorPending: { en: "A progress write is waiting for readback confirmation.", "zh-CN": "进度写入正在等待读回确认。" },
    errorPosition: { en: "The reading position cannot be mapped safely to this EPUB's chapters. Existing progress is preserved.", "zh-CN": "无法将阅读位置可靠地映射到这份 EPUB 的章节，已有进度予以保留。" },
    errorSyncIdentity: { en: "The book's sync identity is invalid. Obtain the EPUB through this bridge's OPDS source.", "zh-CN": "书籍同步身份无效，请通过本桥接服务的 OPDS 源获取 EPUB。" },
    errorIndex: { en: "The chapter index is outside this EPUB's valid range.", "zh-CN": "章节索引超出这份 EPUB 的有效范围。" },
    errorUseOPDS: { en: "Obtain the complete EPUB through OPDS, then open it in Rodel.Reader's main library.", "zh-CN": "请通过 OPDS 获取完整 EPUB，再在小幻主书库中打开。" },
    errorSize: { en: "The EPUB exceeds the configured file, parsing, or cache limits.", "zh-CN": "EPUB 超出配置的文件大小、解析或缓存限制。" },
    errorTransfer: { en: "The EPUB transfer was incomplete. Try again later.", "zh-CN": "EPUB 文件传输不完整，请稍后重试。" },
    errorDerivation: { en: "The EPUB could not be prepared for sync. The source file has not been changed.", "zh-CN": "无法为此 EPUB 准备同步信息，原始文件未被修改。" },
    errorCache: { en: "The cache is busy or full of files currently in use. Try again later.", "zh-CN": "缓存正忙，或容量已被使用中的文件占满，请稍后重试。" },
    errorEncoding: { en: "This EPUB uses an unsupported or invalid text encoding.", "zh-CN": "此 EPUB 的文本编码不受支持或无效。" },
    errorEPUB: { en: "The EPUB structure, resources, or navigation could not be validated. The source file has not been changed.", "zh-CN": "无法验证 EPUB 的结构、资源或目录，原始文件未被修改。" },
    errorRequest: { en: "The service could not accept this request. Check the source configuration.", "zh-CN": "服务无法接受此请求，请检查源配置。" },
    errorCover: { en: "This cover image format is not supported.", "zh-CN": "此封面图片格式不受支持。" },
    errorPageLimit: { en: "The library pagination limit was reached. The book list could not be completed.", "zh-CN": "已达到书库分页上限，无法完整获取书籍列表。" },
};

export const states = { new: "stateNew", bound: "stateBound", paused: "statePaused", pending: "statePending" };

const errorGroups = {
    errorSession: ["NEED_LOGIN"],
    errorCredentials: ["AMBIGUOUS_CREDENTIALS"],
    errorLoginInput: ["INVALID_LOGIN_REQUEST"],
    errorRateLimit: ["LOGIN_RATE_LIMITED"],
    errorUnavailable: ["UPSTREAM_OR_STORAGE_UNAVAILABLE", "INVALID_USER_RESPONSE", "HTTP_500", "HTTP_502", "HTTP_503", "HTTP_504"],
    errorUpstreamLimit: ["UPSTREAM_RATE_LIMITED", "HTTP_429"],
    errorAccess: ["BOOK_ACCESS_DENIED", "HTTP_403"],
    errorNotFound: ["BOOK_NOT_FOUND"],
    errorDownload: ["DOWNLOAD_PERMISSION_REQUIRED"],
    errorIdentity: ["USER_ID_CHANGED", "UPSTREAM_BOOK_ID_MISMATCH"],
    errorHistorical: ["HISTORICAL_PROGRESS_UNBOUND"],
    errorUpstreamProgress: ["UPSTREAM_PROGRESS_AVAILABLE"],
    errorVersion: ["EPUB_VERSION_CHANGED", "EPUB_VERSION_MISMATCH"],
    errorFiles: ["UNSUPPORTED_BOOK_FILES", "BOOK_FILES_CHANGED"],
    errorSource: ["PROGRESS_SOURCE_UNKNOWN"],
    errorReset: ["UPSTREAM_PROGRESS_RESET"],
    errorTime: ["PROGRESS_TIME_UNKNOWN"],
    errorTimeAmbiguous: ["PROGRESS_TIME_AMBIGUOUS"],
    errorUnconfirmed: ["UPSTREAM_WRITE_UNCONFIRMED", "INCOMPLETE_BINDING_WRITE"],
    errorReadback: ["WRITE_READBACK_FAILED", "WRITE_READBACK_MISMATCH"],
    errorInitialRace: ["INITIAL_BINDING_RACE"],
    errorPending: ["WRITE_READBACK_PENDING"],
    errorPosition: ["INVALID_PROGRESS_CFI", "INVALID_PROGRESS_HREF", "CONFLICTING_PROGRESS_POSITION", "INVALID_SECTION_PROGRESS", "AMBIGUOUS_FRAGMENT_PROGRESS", "INVALID_PROGRESS_POSITION", "INVALID_PROGRESS_PERCENT"],
    errorSyncIdentity: ["INVALID_SYNC_IDENTITY"],
    errorIndex: ["INVALID_CHAPTER_INDEX"],
    errorUseOPDS: ["USE_OPDS_EPUB_IMPORT"],
    errorSize: ["EPUB_TOO_LARGE", "EPUB_PARSED_MEMORY_LIMIT", "DERIVED_EPUB_TOO_LARGE", "EPUB_ENTRY_LIMIT", "EPUB_ARCHIVE_LIMIT", "EPUB_DOCUMENT_LIMIT", "EPUB_STRUCTURE_LIMIT"],
    errorTransfer: ["INCOMPLETE_EPUB_TRANSFER"],
    errorDerivation: ["EPUB_DERIVATION_FAILED"],
    errorCache: ["CACHE_BUSY"],
    errorEncoding: ["UNSUPPORTED_EPUB_ENCODING", "INVALID_XML_ENCODING"],
    errorEPUB: ["INVALID_EPUB_ZIP", "UNSAFE_EPUB_ENTRY", "INVALID_EPUB_MIMETYPE", "INVALID_EPUB_XML", "MULTIPLE_XML_ROOTS", "DUPLICATE_EPUB_ANCHOR", "INVALID_XML_END", "INVALID_EPUB_HREF", "INVALID_EPUB_PATH", "INVALID_EPUB_PACKAGE", "INVALID_MANIFEST_ID", "MISSING_EPUB_RESOURCE", "INVALID_EPUB_SPINE", "UNSUPPORTED_SPINE_MEDIA", "EMPTY_EPUB_SPINE", "MISSING_EPUB_NAV", "EMPTY_EPUB_CONTENT", "AMBIGUOUS_NAVIGATION", "NAV_OUTSIDE_SPINE", "INVALID_EPUB_ANCHOR", "INVALID_EPUB_EDIT", "EMPTY_EPUB_METADATA", "MISSING_XHTML_BODY"],
    errorRequest: ["METHOD_NOT_ALLOWED", "JSON_REQUIRED", "INVALID_JSON", "UNSUPPORTED_READER_OPERATION", "INVALID_BOOK_ID", "INVALID_PAGE"],
    errorCover: ["UNSUPPORTED_COVER_MEDIA"],
    errorPageLimit: ["BOOK_PAGE_LIMIT"],
};
export const errorKeys = Object.fromEntries(Object.entries(errorGroups).flatMap(([key, codes]) => codes.map(code => [code, key])));

export function normalizeLocale(value) {
    if (typeof value !== "string") return null;
    if (/^zh(?:-|$)/i.test(value)) return "zh-CN";
    if (/^en(?:-|$)/i.test(value)) return "en";
    return null;
}

export function selectLocale(saved, browserLanguages = []) {
    return normalizeLocale(saved) || browserLanguages.map(normalizeLocale).find(Boolean) || "en";
}

export function translate(key, locale, values = {}) {
    const entry = messages[key];
    if (!entry) throw new Error(`Missing translation: ${key}`);
    return entry[normalizeLocale(locale) || "en"].replace(/\{(\w+)\}/g, (match, name) => Object.hasOwn(values, name) ? String(values[name]) : match);
}

export function errorMessage(code, locale, context) {
    if (context === "login" && (code === "NEED_LOGIN" || code === "HTTP_401")) return translate("loginDenied", locale);
    return translate(errorKeys[code] || "errorUnknown", locale);
}

export function formatTime(value, locale) {
    if (!value) return translate("noSuccess", locale);
    const date = new Date(value);
    if (!Number.isFinite(date.getTime())) return translate("unavailableTime", locale);
    return new Intl.DateTimeFormat(normalizeLocale(locale) || "en", { dateStyle: "medium", timeStyle: "medium" }).format(date);
}
