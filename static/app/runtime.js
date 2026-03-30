function appConfig() {
  return window.__APP_CONFIG || {};
}

function providerDisplayName() {
  var current = String(currentProvider || "").trim().toLowerCase();
  var matched = availableProviders().find(function (item) {
    return item && String(item.id || "").trim().toLowerCase() === current;
  });
  if (matched && matched.displayName) {
    return String(matched.displayName).trim();
  }
  return String(appConfig().providerName || currentProvider || "Code").trim();
}

function setNodeText(node, value) {
  if (!node) return;
  node.textContent = value;
}

function providerRequiresAuth() {
  return Boolean(appConfig().requiresAuth);
}

function providerRequiresAuthFor(providerID) {
  return String(providerID || "").toLowerCase() === "codex";
}

function providerSupportsFast() {
  return String(currentProvider || "").toLowerCase() === "codex";
}

function providerSupportsCompact() {
  return String(currentProvider || "").toLowerCase() === "codex";
}

function availableProviders() {
  return Array.isArray(appConfig().providers) ? appConfig().providers : [];
}

function providerSessionStorageKeys() {
  return ["codex_session_id", "sessionId", "sessionid"];
}

function clearStoredSession() {
  providerSessionStorageKeys().forEach(function (key) {
    localStorage.removeItem(key);
    sessionStorage.removeItem(key);
  });
}

function sessionRefsStorageKey() {
  return "code_web_session_refs";
}

function currentSessionRefStorageKey() {
  return "code_web_current_session_ref";
}

function sessionCacheStorageKey() {
  return "code_web_session_cache";
}

function parseJSONSafe(raw, fallback) {
  try {
    return JSON.parse(raw);
  } catch (err) {
    return fallback;
  }
}

function apiPayload(json) {
  if (!json || typeof json !== "object") return {};
  if (!Object.prototype.hasOwnProperty.call(json, "data")) return json;
  return json.data || {};
}

async function apiJSON(res) {
  var json = await res.json().catch(function () { return null; });
  if (!res.ok) {
    var message = json && json.message ? json.message : (await res.text().catch(function () { return ""; }));
    throw new Error(message || "请求失败");
  }
  if (json && typeof json.status === "number" && json.status !== 0) {
    throw new Error(json.message || "请求失败");
  }
  return apiPayload(json);
}

function loadSessionRefs() {
  var items = parseJSONSafe(localStorage.getItem(sessionRefsStorageKey()) || "[]", []);
  return Array.isArray(items) ? items.filter(function (item) { return item && item.provider; }) : [];
}

function loadSessionCache() {
  var items = parseJSONSafe(localStorage.getItem(sessionCacheStorageKey()) || "{}", {});
  return items && typeof items === "object" ? items : {};
}

function saveSessionCache(items) {
  localStorage.setItem(sessionCacheStorageKey(), JSON.stringify(items && typeof items === "object" ? items : {}));
}

function saveSessionRefs(items) {
  localStorage.setItem(sessionRefsStorageKey(), JSON.stringify(Array.isArray(items) ? items : []));
}

function sessionRefIdentity(item) {
  if (!item) return "";
  if (item.restoreRef && typeof item.restoreRef === "object") {
    return sessionRefIdentity(item.restoreRef);
  }
  var provider = String(item.provider || "").trim().toLowerCase();
  var remote = String(item.codexThreadId || item.providerSessionId || item.refId || item.id || "").trim();
  if (!provider || !remote) return "";
  return provider + "|" + remote;
}

function normalizeSessionRef(ref) {
  if (!ref) return null;
  var source = ref && ref.restoreRef && typeof ref.restoreRef === "object" ? ref.restoreRef : ref;
  var provider = String(source.provider || "").trim().toLowerCase();
  var codexThreadId = String(source.codexThreadId || "").trim();
  var providerSessionId = String(source.providerSessionId || "").trim();
  if (!provider || (!codexThreadId && !providerSessionId)) return null;
  return {
    refId: String(source.refId || sessionRefIdentity({ provider: provider, codexThreadId: codexThreadId, providerSessionId: providerSessionId })).trim(),
    localSessionId: String(source.localSessionId || source.sessionId || ref.localSessionId || ref.sessionId || "").trim(),
    provider: provider,
    model: String(source.model || ref.model || "").trim(),
    workdir: String(source.workdir || source.cwd || ref.workdir || ref.cwd || "").trim(),
    codexThreadId: codexThreadId,
    providerSessionId: providerSessionId,
    updatedAt: ref.updatedAt || source.updatedAt || new Date().toISOString(),
    lastMessage: String(ref.lastMessage || source.lastMessage || "").trim(),
    lastEvent: String(ref.lastEvent || source.lastEvent || "").trim(),
    messageCount: Number(ref.messageCount || source.messageCount || 0),
    running: Boolean(ref.running || source.running)
  };
}

function upsertSessionRef(ref) {
  var normalized = normalizeSessionRef(ref);
  if (!normalized) return null;
  var items = loadSessionRefs();
  var key = normalized.refId;
  var existing = items.find(function (item) { return sessionRefIdentity(item) === key; }) || null;
  var merged = existing ? {
    refId: normalized.refId,
    localSessionId: normalized.localSessionId || existing.localSessionId || "",
    provider: normalized.provider,
    model: normalized.model || existing.model || "",
    workdir: normalized.workdir || existing.workdir || "",
    codexThreadId: normalized.codexThreadId || existing.codexThreadId || "",
    providerSessionId: normalized.providerSessionId || existing.providerSessionId || "",
    updatedAt: normalized.updatedAt || existing.updatedAt || new Date().toISOString(),
    lastMessage: normalized.lastMessage || existing.lastMessage || "",
    lastEvent: normalized.lastEvent || existing.lastEvent || "",
    messageCount: normalized.messageCount || existing.messageCount || 0,
    running: normalized.running
  } : normalized;
  var nextItems = items.filter(function (item) { return sessionRefIdentity(item) !== key; });
  nextItems.unshift(merged);
  saveSessionRefs(nextItems.slice(0, 50));
  localStorage.setItem(currentSessionRefStorageKey(), JSON.stringify(merged));
  return merged;
}

function removeSessionRef(refLike) {
  var key = sessionRefIdentity(refLike) || String(refLike || "").trim();
  if (!key) return;
  var removedKeys = [];
  var nextItems = loadSessionRefs().filter(function (item) {
    var normalized = normalizeSessionRef(item);
    var shouldKeep = sessionRefIdentity(item) !== key &&
      String(item.refId || "") !== key &&
      String((normalized && normalized.localSessionId) || "").trim() !== key;
    if (!shouldKeep) {
      var identity = sessionRefIdentity(item);
      if (identity) removedKeys.push(identity);
    }
    return shouldKeep;
  });
  saveSessionRefs(nextItems);
  var currentRef = getCurrentSessionRef();
  if (currentRef && (sessionRefIdentity(currentRef) === key || String(currentRef.localSessionId || "").trim() === key)) {
    localStorage.removeItem(currentSessionRefStorageKey());
  }
  var cache = loadSessionCache();
  removedKeys.concat([key]).forEach(function (cacheKey) {
    if (Object.prototype.hasOwnProperty.call(cache, cacheKey)) {
      delete cache[cacheKey];
    }
  });
  saveSessionCache(cache);
}

function getCurrentSessionRef() {
  return normalizeSessionRef(parseJSONSafe(localStorage.getItem(currentSessionRefStorageKey()) || "null", null));
}

function findSessionRefByLocalSessionId(sessionId) {
  var target = String(sessionId || "").trim();
  if (!target) return null;
  var items = loadSessionRefs();
  for (var i = 0; i < items.length; i += 1) {
    var item = normalizeSessionRef(items[i]);
    if (item && String(item.localSessionId || "").trim() === target) {
      return item;
    }
  }
  return null;
}

function setCurrentSessionRef(ref) {
  var normalized = normalizeSessionRef(ref);
  if (!normalized) {
    localStorage.removeItem(currentSessionRefStorageKey());
    return;
  }
  localStorage.setItem(currentSessionRefStorageKey(), JSON.stringify(normalized));
}

function storedSessionList() {
  return loadSessionRefs().map(function (item) {
    return {
      id: "ref:" + item.refId,
      localSessionId: item.localSessionId,
      provider: item.provider,
      model: item.model,
      workdir: item.workdir,
      codexThreadId: item.codexThreadId,
      providerSessionId: item.providerSessionId,
      updatedAt: item.updatedAt,
      lastMessage: item.lastMessage,
      lastEvent: item.lastEvent,
      messageCount: item.messageCount,
      running: item.running,
      restoreRef: item,
      isStoredRef: true
    };
  });
}

function rememberSessionRef(ref) {
  return upsertSessionRef(ref);
}

function rememberSessionSnapshot(session, meta) {
  if (!session) return null;
  var ref = rememberSessionRef(session.restoreRef || {
    localSessionId: session.id,
    provider: session.provider || (meta && meta.provider) || currentProvider,
    model: session.model || (meta && meta.model) || "",
    workdir: session.workdir || (meta && meta.cwd) || "",
    codexThreadId: session.codexThreadId,
    providerSessionId: session.providerSessionId,
    updatedAt: session.updatedAt || new Date().toISOString(),
    lastMessage: resumeSummary({ lastMessage: lastUserMessageText(session.messages || []) }),
    lastEvent: resumeSummary({ lastEvent: lastEventText(session.events || []) }),
    messageCount: Array.isArray(session.messages) ? session.messages.length : 0,
    running: Boolean(session.activeTaskId)
  });
  if (ref) {
    var cache = loadSessionCache();
    cache[ref.refId] = {
      updatedAt: session.updatedAt || new Date().toISOString(),
      messages: Array.isArray(session.messages) ? session.messages : [],
      events: Array.isArray(session.events) ? session.events : [],
      draftMessage: session.draftMessage || null
    };
    var keys = Object.keys(cache).sort(function (a, b) {
      var left = cache[a] && cache[a].updatedAt ? new Date(cache[a].updatedAt).getTime() : 0;
      var right = cache[b] && cache[b].updatedAt ? new Date(cache[b].updatedAt).getTime() : 0;
      return right - left;
    }).slice(0, 20);
    var trimmed = {};
    keys.forEach(function (key) {
      trimmed[key] = cache[key];
    });
    saveSessionCache(trimmed);
  }
  return ref;
}

function getSessionCache(refLike) {
  var key = sessionRefIdentity(refLike) || String(refLike || "").trim();
  if (!key) return null;
  var cache = loadSessionCache();
  return cache[key] || null;
}

function lastUserMessageText(messages) {
  for (var i = (messages || []).length - 1; i >= 0; i--) {
    var item = messages[i];
    if (item && item.role === "user" && item.content) return item.content;
  }
  return "";
}

function lastEventText(events) {
  for (var i = (events || []).length - 1; i >= 0; i--) {
    var item = events[i];
    if (item && (item.target || item.body || item.title)) return item.target || item.body || item.title;
  }
  return "";
}

function providerIcon(providerID, displayName) {
  var id = String(providerID || "").trim().toLowerCase();
  if (id === "claude") {
    return '' +
      '<svg viewBox="0 0 48 48" aria-hidden="true">' +
        '<circle cx="24" cy="24" r="10"></circle>' +
        '<path d="M24 5v7M24 36v7M5 24h7M36 24h7M11 11l5 5M32 32l5 5M11 37l5-5M32 16l5-5"></path>' +
      '</svg>';
  }
  return '' +
    '<svg viewBox="0 0 48 48" aria-hidden="true">' +
      '<path d="M34 12c-2.5-3-6-4.5-10.5-4.5C14.4 7.5 8 13.7 8 24s6.4 16.5 15.5 16.5c4.5 0 8-1.5 10.5-4.5"></path>' +
      '<path d="M30 16h8v16h-8"></path>' +
      '<path d="M22 16a8 8 0 1 0 0 16"></path>' +
    '</svg>';
}

function connectionStateCopy(state) {
  switch (String(state || "").toLowerCase()) {
    case "connecting":
      return { badge: "link", title: "正在建立连接", detail: "正在与当前会话建立实时通道。" };
    case "reconnecting":
      return { badge: "retry", title: "正在恢复连接", detail: "实时连接已断开，系统会自动重试并尽量恢复原会话。" };
    case "restoring":
      return { badge: "restore", title: "正在恢复远端会话", detail: "正在用已保存的 restore ref 重建会话上下文。" };
    case "error":
      return { badge: "error", title: "连接异常", detail: "实时通道暂不可用，可稍后重试或重新恢复会话。" };
    default:
      return { badge: "live", title: "连接正常", detail: "实时事件和消息流会显示在这里。" };
  }
}

function setConnectionBanner(state, detail) {
  if (!connectionBanner || !connectionBadge || !connectionTitle || !connectionDetail) return;
  var next = connectionStateCopy(state);
  connectionBanner.hidden = String(state || "").toLowerCase() === "connected";
  connectionBanner.dataset.state = String(state || "connected").toLowerCase();
  connectionBadge.textContent = next.badge;
  connectionTitle.textContent = next.title;
  connectionDetail.textContent = String(detail || next.detail || "").trim();
}

function sessionKindLabel(item) {
  if (!item) return "live";
  if (item.isStoredRef || item.restoreRef) return "restored";
  return "live";
}

function sessionMetaChips(item) {
  var chips = [];
  if (item && item.updatedAt) chips.push("更新 " + formatTime(item.updatedAt));
  if (item && item.messageCount) chips.push("消息 " + item.messageCount);
  if (item && item.workdir) chips.push(compact(item.workdir));
  if (item && item.restoreRef) chips.push("含缓存");
  if (item && item.running) chips.push("运行中");
  return chips.slice(0, 4);
}

function syncProviderPicker() {
  if (!providerPicker) return;
  var buttons = providerPicker.querySelectorAll("[data-provider-id]");
  Array.from(buttons).forEach(function (button) {
    var selected = String(button.dataset.providerId || "") === String(currentProvider || "");
    button.classList.toggle("is-selected", selected);
    button.setAttribute("aria-checked", selected ? "true" : "false");
  });
}

function setCurrentProvider(providerID) {
  var nextProvider = String(providerID || "").trim().toLowerCase();
  if (!nextProvider) return;
  currentProvider = nextProvider;
  syncProviderPicker();
}

function populateProviderSelect() {
  if (!providerPicker) return;
  var items = availableProviders().filter(function (item) { return item && item.available; });
  providerPicker.innerHTML = "";
  items.forEach(function (item) {
    var button = document.createElement("button");
    button.type = "button";
    button.className = "provider-option";
    button.dataset.providerId = item.id;
    button.setAttribute("role", "radio");
    button.setAttribute("aria-label", item.displayName);
    button.innerHTML =
      '<span class="provider-option-icon">' + providerIcon(item.id, item.displayName) + '</span>' +
      '<span class="provider-option-name">' + item.displayName + '</span>';
    button.addEventListener("click", function () {
      setCurrentProvider(item.id);
    });
    providerPicker.appendChild(button);
  });
  var defaultItem = items.find(function (item) { return item.isDefault; }) || items[0];
  if (defaultItem && !items.some(function (item) { return item.id === currentProvider; })) {
    currentProvider = defaultItem.id;
  }
  syncProviderPicker();
}

function setTransportState(state) {
  setNodeText(transportBadge, state);
  setNodeText(desktopTransportBadge, state);
  setNodeText(statusTransport, state);
  setConnectionBanner(state);
  if (!isRunning) {
    setFooterStatus(state === "connected" ? "ready" : state, transportDetail(state));
  }
}

function showLoginScreen() {
  isAuthenticated = false;
  document.body.classList.add("auth-required");
  loginScreen.hidden = false;
  sessionChooser.hidden = true;
  codexAuthScreen.hidden = true;
  loginError.textContent = "";
  timeline.innerHTML = "";
  removeWorkingPlaceholder();
  setTimeout(function () {
    passwordInput.focus();
  }, 0);
}

function hideLoginScreen() {
  isAuthenticated = true;
  document.body.classList.remove("auth-required");
  loginScreen.hidden = true;
  loginError.textContent = "";
  passwordInput.value = "";
}

function showSessionChooser() {
  document.body.classList.add("auth-required");
  sessionChooser.hidden = false;
  codexAuthScreen.hidden = true;
  resumeList.hidden = true;
  resumeList.innerHTML = "";
  resumeEmpty.hidden = true;
  setConnectionBanner("connected");
}

function hideSessionChooser() {
  document.body.classList.remove("auth-required");
  sessionChooser.hidden = true;
  resumeList.hidden = true;
  resumeList.innerHTML = "";
  resumeEmpty.hidden = true;
}

function buildCodexAuthLink() {
  return "/codex-auth";
}

function authGuideStepsConfig() {
  var config = window.__APP_CONFIG || {};
  return Array.isArray(config.authGuideSteps) ? config.authGuideSteps : [];
}

async function submitCodexAuthCallback(callbackUrl) {
  var res = await fetch("/api/codex-auth/complete", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ sessionId: currentCodexAuthSessionId, callbackUrl: String(callbackUrl || "").trim() }),
  });
  var data = await res.json();
  if (!res.ok && (!data || !data.session || !data.session.error)) {
    throw new Error("提交回调链接失败");
  }
  return data;
}

async function openCodexAuthLink() {
  var params = new URLSearchParams();
  params.set("restart", "1");
  var query = "?" + params.toString();
  var res = await fetch("/api/codex-auth/start" + query, { method: "POST", credentials: "same-origin" });
  var data = await res.json();
  if (!res.ok) {
    throw new Error((data && data.session && data.session.error) || "生成授权链接失败");
  }
  return data;
}

function renderAuthGuide(container, buttonId, buttonClass) {
  if (!container) return;
  var steps = authGuideStepsConfig();
  container.innerHTML = "";
  steps.forEach(function (text, index) {
    var row = document.createElement("div");
    row.className = "resume-item auth-guide-item";
    var buttonHtml = index === 0
      ? '<div class="auth-guide-launch"><button id="' + buttonId + '" class="' + buttonClass + '" type="button">打开授权页面</button></div>'
      : "";
    row.innerHTML = '<div class="resume-open resume-open-static">' +
      '<div class="resume-item-title">步骤 ' + (index + 1) + '</div>' +
      '<div class="resume-item-desc">' + text + '</div>' +
      buttonHtml +
      '</div>';
    container.appendChild(row);
  });
}

async function showCodexAuthScreen(message) {
  document.body.classList.add("auth-required");
  codexAuthScreen.hidden = false;
  loginScreen.hidden = true;
  sessionChooser.hidden = true;
  if (authTitle) authTitle.textContent = "ChatGPT 账户验证";
  if (authIntro) {
    authIntro.textContent = message || ("当前机器上的 " + providerDisplayName() + " 尚未完成 ChatGPT 账户验证，或验证已失效。");
  }
  currentCodexAuthSessionId = "";
  renderAuthGuide(codexAuthSteps, "codexAuthLink", "login-button login-button-compact");
  if (codexAuthInput) codexAuthInput.value = "";
  codexAuthLink = document.getElementById("codexAuthLink");
  if (codexAuthHint) {
    codexAuthHint.textContent = "";
  }
  var status = await checkCodexAuthStatus().catch(function () { return null; });
  if (status && status.loggedIn) {
    if (codexAuthLink) {
      codexAuthLink.disabled = true;
      codexAuthLink.textContent = "当前设备已登录";
    }
    if (codexAuthHint) {
      codexAuthHint.textContent = "";
    }
    return;
  }
  if (status && status.session) {
    if (status.session.id) {
      currentCodexAuthSessionId = status.session.id;
    }
    if (codexAuthHint && status.session.error) {
      codexAuthHint.textContent = "";
    }
  }
}

function hideCodexAuthScreen() {
  codexAuthScreen.hidden = true;
}

function isCodexAuthError(message) {
  var text = String(message || "").toLowerCase();
  return text.includes("not logged in") ||
    text.includes("codex login") ||
    text.includes("authentication") ||
    text.includes("unauthorized") ||
    text.includes("login required") ||
    text.includes("logged out") ||
    text.includes("expired");
}

function setTaskState(running) {
  isRunning = running;
  imageBtn.disabled = running;
  updateSendState();
  setNodeText(statusTask, running ? "running" : "idle");
  if (running) {
    setFooterStatus("Working", providerDisplayName() + " 正在执行任务，可输入 /stop 终止");
    input.placeholder = "发送消息...";
    return;
  }
  setFooterStatus(transportBadge.textContent === "connected" ? "ready" : transportBadge.textContent, "等待输入");
  input.placeholder = "发送消息...";
}

function setSession(id) {
  currentSessionId = id;
  localStorage.setItem("codex_session_id", id);
  localStorage.setItem("sessionId", id);
  localStorage.setItem("sessionid", id);
  setNodeText(sessionBadge, id.slice(0, 8));
  setNodeText(desktopSessionBadge, id.slice(0, 8));
  setNodeText(statusSession, shortSession(id));
}

function setMeta(meta) {
  if (!meta) return;
  if (meta.provider) {
    setCurrentProvider(meta.provider);
    setNodeText(statusProvider, meta.provider);
  }
  if (meta.model) setNodeText(modelBadge, meta.model);
  if (meta.cwd) setNodeText(cwdBadge, meta.cwd);
  if (meta.model) setNodeText(statusModel, meta.model);
  if (meta.cwd) setNodeText(statusCwd, meta.cwd);
  setNodeText(statusApprovals, meta.approvalPolicy || (statusApprovals && statusApprovals.textContent) || "never");
  setNodeText(statusFast, meta.fastMode ? "on" : "off");
  setNodeText(statusServiceTier, meta.serviceTier || "default");
}

function autoResize() {
  input.style.height = "auto";
  input.style.height = Math.min(input.scrollHeight, 132) + "px";
}

function updateSendState() {
  var hasContent = String(input.value || "").trim().length > 0;
  var hasImages = pendingImages.length > 0;
  var commandToken = commandQuery(input.value || "");
  var exactCommand = commands.find(function (item) {
    return item.name === commandToken || (item.aliases || []).includes(commandToken);
  });
  var canSubmitWhileRunning = Boolean(exactCommand && exactCommand.name === "/stop");
  sendBtn.disabled = (isRunning && !canSubmitWhileRunning) || (!hasContent && !hasImages);
}

function canAcceptImageFile(file) {
  return Boolean(file && typeof file.type === "string" && file.type.toLowerCase().startsWith("image/"));
}

function addPendingImageFiles(files) {
  var added = false;
  (files || []).forEach(function (file) {
    if (!canAcceptImageFile(file)) {
      return;
    }
    pendingImages.push({
      file: file,
      url: URL.createObjectURL(file),
    });
    added = true;
  });
  if (!added) {
    return false;
  }
  renderAttachmentTray();
  updateSendState();
  return true;
}

function compact(text) {
  return String(text || "").replace(/\s+/g, " ").trim().slice(0, 120) || "等待输入";
}

function applyBuildInfo() {
  var config = appConfig();
  var version = String(config.version || "dev").trim();
  if (!version) version = "dev";
  if (version.charAt(0) !== "v") {
    version = "v" + version;
  }
  document.title = String(config.appName || "Code Web").trim() || "Code Web";
  Array.from(appTitleNodes || []).forEach(function (node) {
    node.textContent = String(config.appName || "Code Web").trim() || "Code Web";
  });
  Array.from(versionNodes || []).forEach(function (node) {
    node.textContent = version;
  });
  populateProviderSelect();
}

function showError(message) {
  var text = compact(message || "操作失败");
  setFooterStatus("error", text);
  if (providerRequiresAuth() && isCodexAuthError(text)) {
    showCodexAuthScreen(providerDisplayName() + " 授权已失效，请重新授权。");
  }
  if (!errorToast) {
    return;
  }
  errorToast.textContent = text;
  errorToast.hidden = false;
  clearTimeout(errorToastTimer);
  errorToastTimer = setTimeout(function () {
    errorToast.hidden = true;
  }, 3200);
}

function shouldRenderMarkdown(message) {
  return Boolean(message && message.role === "assistant");
}

function renderMarkdown(node, text) {
  var bubble = node.querySelector(".bubble");
  var source = String(text || "");
  var markedLib = window.marked;
  var purifier = window.DOMPurify;
  if (!markedLib || !purifier) {
    bubble.textContent = source;
    return;
  }
  markedLib.setOptions({
    breaks: true,
    gfm: true,
  });
  var html = markedLib.parse(source);
  bubble.innerHTML = purifier.sanitize(html);
}

function firstLine(text) {
  return String(text || "").split("\n")[0];
}

function transportDetail(state) {
  if (state === "connected") return "等待输入";
  if (state === "connecting") return "正在建立连接";
  if (state === "reconnecting") return "正在恢复连接";
  return "连接不可用";
}

function formatTime(value) {
  if (!value) return "--:--";
  return new Date(value).toLocaleString("zh-CN", {
    hour12: false,
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function formatReset(ts) {
  if (!ts) return "unknown";
  return new Date(ts * 1000).toLocaleString("zh-CN", {
    hour12: false,
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function remainText(windowData) {
  var used = Number(windowData.usedPercent || 0);
  var remain = Math.max(0, 100 - used);
  return remain + "% left, used " + used + "%, reset " + formatReset(windowData.resetsAt);
}

function creditText(credits) {
  if (credits.unlimited) return "unlimited";
  if (credits.hasCredits && credits.balance != null) return String(credits.balance);
  return "none";
}

async function checkCodexAuthStatus() {
  if (!providerRequiresAuth()) {
    return { loggedIn: true };
  }
  var res = await fetch("/api/codex-auth/status", { credentials: "same-origin" });
  return apiJSON(res);
}

function setFooterStatus(state, detail) {
  renderFooterState(state);
  if (detail != null) {
    footerDetail.textContent = detail;
  }
}

function renderFooterState(state) {
  var text = String(state || "").trim() || "ready";
  footerState.textContent = "";
  footerState.classList.toggle("is-animated", text.toLowerCase() === "working");
  if (text.toLowerCase() !== "working") {
    footerState.textContent = text;
    return;
  }
  var wrap = document.createElement("span");
  wrap.className = "working-text working-marquee";
  Array.from(text).forEach(function (char, index) {
    var node = document.createElement("span");
    node.className = "working-char";
    node.style.animationDelay = (index * 0.12) + "s";
    node.textContent = char;
    wrap.appendChild(node);
  });
  footerState.appendChild(wrap);
}

function ensureWorkingPlaceholder() {
  return;
}

function removeWorkingPlaceholder() {
  return;
}

function shortSession(id) {
  return id ? String(id).slice(0, 8) : "unknown";
}

function resumeSummary(item) {
  var parts = [];
  if (item && item.provider) parts.push(String(item.provider));
  if (item.running) parts.push("运行中");
  parts.push(compact(item.workdir || "") || "无工作目录");
  if (item.lastMessage) {
    parts.push(compact(item.lastMessage));
  } else if (item.lastEvent) {
    parts.push(compact(item.lastEvent));
  } else {
    parts.push("无消息记录");
  }
  parts.push(Number(item.messageCount || 0) + " 条消息");
  parts.push(item.updatedAt ? formatTime(item.updatedAt) : "--:--");
  return parts.join(" · ");
}

function resumeWorkdir(item) {
  return compact(item && item.workdir || "") || "无工作目录";
}

function resumeActivity(item) {
  if (item && item.lastMessage) {
    return compact(item.lastMessage);
  }
  if (item && item.lastEvent) {
    return compact(item.lastEvent);
  }
  return "无消息记录";
}

function createNode(tag, className, text) {
  var node = document.createElement(tag);
  if (className) node.className = className;
  if (text != null) node.textContent = text;
  return node;
}

function resumeBadges(item) {
  var badges = [];
  badges.push({ text: sessionKindLabel(item), className: "resume-item-badge is-" + sessionKindLabel(item) });
  if (item && item.running) {
    badges.push({ text: "running", className: "resume-item-badge is-running" });
  }
  return badges;
}

function renderResumeList(items, options) {
  if (!resumeList) return;
  var settings = options || {};
  resumeList.innerHTML = "";
  resumeList.hidden = false;
  (items || []).forEach(function (item) {
    var row = createNode("div", "resume-item");
    var openButton = createNode("button", "resume-open");
    openButton.type = "button";

    var head = createNode("div", "resume-item-head");
    var provider = createNode("div", "resume-item-provider");
    var icon = createNode("span", "resume-provider-icon");
    icon.innerHTML = providerIcon(item && item.provider, item && item.provider);
    provider.appendChild(icon);

    var titleWrap = createNode("div");
    var title = createNode("div", "resume-item-title", shortSession(item && item.id));
    titleWrap.appendChild(title);
    if (item && item.provider) {
      titleWrap.appendChild(createNode("div", "resume-item-desc", String(item.provider).toUpperCase()));
    }
    provider.appendChild(titleWrap);
    head.appendChild(provider);

    var badgeWrap = createNode("div");
    resumeBadges(item).forEach(function (badge) {
      badgeWrap.appendChild(createNode("span", badge.className, badge.text));
    });
    head.appendChild(badgeWrap);
    openButton.appendChild(head);

    openButton.appendChild(createNode("div", "resume-item-path", resumeWorkdir(item)));

    var summary = createNode("div", "resume-item-summary");
    var latest = createNode("div", "resume-summary-block");
    latest.appendChild(createNode("div", "resume-summary-label", "Latest"));
    latest.appendChild(createNode("div", "resume-summary-value", resumeActivity(item)));
    summary.appendChild(latest);

    var context = createNode("div", "resume-summary-block");
    context.appendChild(createNode("div", "resume-summary-label", "Context"));
    context.appendChild(createNode("div", "resume-summary-value", Number(item && item.messageCount || 0) + " 条消息"));
    summary.appendChild(context);
    openButton.appendChild(summary);

    var meta = createNode("div", "resume-item-meta");
    sessionMetaChips(item).forEach(function (chip) {
      meta.appendChild(createNode("span", "resume-meta-chip", chip));
    });
    openButton.appendChild(meta);

    openButton.addEventListener("click", async function () {
      try {
        openButton.disabled = true;
        openButton.classList.add("is-loading");
        if (typeof settings.onOpen === "function") {
          await settings.onOpen(item, openButton);
        }
      } catch (err) {
        openButton.disabled = false;
        openButton.classList.remove("is-loading");
        throw err;
      }
    });

    var deleteButton = createNode("button", "resume-delete", "×");
    deleteButton.type = "button";
    deleteButton.setAttribute("aria-label", "删除会话");
    deleteButton.addEventListener("click", async function (evt) {
      evt.stopPropagation();
      if (typeof settings.onDelete === "function") {
        await settings.onDelete(item, row);
      }
    });

    row.appendChild(openButton);
    row.appendChild(deleteButton);
    resumeList.appendChild(row);
  });
  if (!items || !items.length) {
    resumeList.hidden = true;
  }
}
