var restoreReconnectInFlight = false;

function debugRestoreLog(label, payload) {
  try {
    console.info("[restore]", label, payload || {});
  } catch (err) {}
}

function waitMs(ms) {
  return new Promise(function (resolve) {
    setTimeout(resolve, Math.max(0, Number(ms || 0)));
  });
}

async function restoreSessionWithRetry(ref, connectNow, options) {
  var settings = options || {};
  var attempts = Math.max(1, Number(settings.attempts || 3));
  var delayMs = Math.max(0, Number(settings.delayMs || 1200));
  var lastErr = null;
  for (var attempt = 1; attempt <= attempts; attempt += 1) {
    try {
      debugRestoreLog("restore_attempt", { attempt: attempt, attempts: attempts });
      return await restoreSession(ref, connectNow);
    } catch (err) {
      lastErr = err;
      debugRestoreLog("restore_attempt_failed", {
        attempt: attempt,
        attempts: attempts,
        message: err && err.message ? err.message : String(err || "")
      });
      if (attempt >= attempts) {
        break;
      }
      await waitMs(delayMs * attempt);
    }
  }
  throw lastErr || new Error("恢复远端会话失败");
}

function ensureSocket() {
  clearTimeout(reconnectTimer);
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    return;
  }
  if (ws) {
    wsIntentionalClose = true;
    try {
      ws.close();
    } catch (err) {}
    ws = null;
  }
  connect();
}

async function prepareSessionForSocket() {
  var savedSessionId = String(currentSessionId || "").trim();
  if (!savedSessionId || restoreReconnectInFlight) {
    return;
  }
  var items = await fetchSessions().catch(function () { return []; });
  var liveMatch = items.find(function (item) {
    return item && !item.isStoredRef && String(item.id || "").trim() === savedSessionId;
  });
  if (liveMatch) {
    debugRestoreLog("prepare_session_live_match", { sessionId: savedSessionId });
    return;
  }
  var cachedRef = getCurrentSessionRef() || findSessionRefByLocalSessionId(savedSessionId);
  debugRestoreLog("prepare_session_check", {
    sessionId: savedSessionId,
    hasLiveMatch: false,
    cachedRef: cachedRef ? {
      refId: cachedRef.refId,
      localSessionId: cachedRef.localSessionId,
      provider: cachedRef.provider
    } : null
  });
  if (!cachedRef) {
    return;
  }
  restoreReconnectInFlight = true;
  try {
    setConnectionBanner("restoring", "正在恢复 " + shortSession(savedSessionId) + " 的远端引用。");
    await restoreSessionWithRetry(cachedRef, false, { attempts: 4, delayMs: 1200 });
  } finally {
    restoreReconnectInFlight = false;
  }
}

async function createSession(workdir, connectNow, provider) {
  var nextProvider = String(provider || currentProvider || "").trim();
  var res = await fetch("/api/session/new", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ workdir: String(workdir || "").trim(), provider: nextProvider }),
  });
  var data = await apiJSON(res);
  setCurrentProvider(nextProvider || currentProvider);
  setCurrentSessionRef(null);
  setSession(data.sessionId);
  replaceTimeline([], []);
  setTaskState(false);
  setFooterStatus("ready", "等待输入");
  if (ws) {
    var current = ws;
    ws = null;
    wsIntentionalClose = true;
    current.close();
  }
  if (connectNow !== false) {
    ensureSocket();
  }
}

async function submitPrompt(raw) {
  var content = (raw == null ? input.value : raw).trim();
  if ((!content && !pendingImages.length) || !currentSessionId) return;
  var commandToken = commandQuery(content);
  var exactCommand = commands.find(function (item) {
    return item.name === commandToken || (item.aliases || []).includes(commandToken);
  });
  if (isRunning && (!exactCommand || exactCommand.name !== "/stop")) return;
  if (exactCommand) {
    await executeCommand(exactCommand);
    return;
  }
  hideCommandPalette();
  var formData = new FormData();
  formData.append("sessionId", currentSessionId);
  formData.append("content", content);
  pendingImages.forEach(function (item) {
    formData.append("images", item.file, item.file.name);
  });

  ensureSocket();
  setTaskState(true);
  setFooterStatus("Working", compact(content || "发送图片"));
  ensureWorkingPlaceholder();
  try {
    var res = await fetch("/api/send", { method: "POST", body: formData });
    if (!res.ok) {
      throw new Error(await res.text());
    }
    input.value = "";
    pendingImages = [];
    renderAttachmentTray();
    autoResize();
    hideCommandPalette();
  } catch (err) {
    removeWorkingPlaceholder();
    setTaskState(false);
    showError(err && err.message ? err.message : "发送失败");
  }
}

function connect() {
  clearTimeout(reconnectTimer);
  setTransportState("connecting");
  Promise.resolve()
    .then(function () {
      return prepareSessionForSocket();
    })
    .then(function () {
      if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
        return;
      }
      var protocol = location.protocol === "https:" ? "wss:" : "ws:";
      wsIntentionalClose = false;
      ws = new WebSocket(protocol + "//" + location.host + "/ws");
      var socket = ws;

      socket.addEventListener("open", function () {
        if (ws !== socket) return;
        wsIntentionalClose = false;
        setTransportState("connected");
        setConnectionBanner("connecting", "握手已建立，正在同步会话快照。");
        debugRestoreLog("ws_open", { sessionId: currentSessionId });
        socket.send(JSON.stringify({ type: "hello", sessionId: currentSessionId }));
      });

      socket.addEventListener("message", function (evt) {
        if (ws !== socket) return;
        var data = JSON.parse(evt.data);
        if (data.type === "snapshot" && data.session) {
          debugRestoreLog("snapshot", {
            currentSessionId: currentSessionId,
            incomingSessionId: String(data.session.id || "").trim(),
            messageCount: Array.isArray(data.session.messages) ? data.session.messages.length : -1,
            eventCount: Array.isArray(data.session.events) ? data.session.events.length : -1,
            restoreRef: data.session.restoreRef || null
          });
          setSession(data.session.id);
          setMeta(data.meta);
          replaceTimeline(data.session.messages || [], data.session.events || [], data.session.draftMessage || null);
          rememberSessionSnapshot(data.session, data.meta || {});
          setTaskState(Boolean(data.running));
          setConnectionBanner("connected", "已连接到 " + shortSession(data.session.id) + "，实时事件同步正常。");
          if (data.running && !data.session.draftMessage) {
            ensureWorkingPlaceholder();
          }
          return;
        }
        if (data.type === "meta_update" && data.meta) {
          setMeta(data.meta);
          return;
        }
        if (data.type === "message" && data.message) {
          renderMessage(data.message, { animate: false });
          return;
        }
        if (data.type === "message_delta" && data.message) {
          removeWorkingPlaceholder();
          renderMessage(data.message, { draft: true, animate: false });
          return;
        }
        if (data.type === "message_final" && data.message) {
          removeWorkingPlaceholder();
          removeOtherDrafts(data.message.id);
          renderMessage(data.message, { draft: false, animate: false });
          return;
        }
        if (data.type === "log" && data.log) {
          if (eventBody(data.log)) {
            renderEvent(data.log, { animate: false });
          }
          return;
        }
        if (data.type === "task_status") {
          setTaskState(Boolean(data.running));
          if (data.running) {
            setConnectionBanner("connected", "会话正在执行任务，事件流保持同步。");
          }
          if (data.running) {
            ensureWorkingPlaceholder();
          } else {
            removeWorkingPlaceholder();
          }
          return;
        }
        if (data.type === "error" && data.error) {
          showError(data.error);
        }
      });

      socket.addEventListener("close", function () {
        if (ws === socket) {
          ws = null;
        }
        if (wsIntentionalClose) {
          wsIntentionalClose = false;
          return;
        }
        setTransportState("reconnecting");
        setConnectionBanner("reconnecting", "实时连接已断开，1.5 秒后自动重试。");
        showError("连接已断开，正在重连");
        reconnectTimer = setTimeout(connect, 1500);
      });

      socket.addEventListener("error", function () {
        if (ws !== socket) return;
        setTransportState("error");
        setConnectionBanner("error", "WebSocket 连接异常，稍后会继续尝试恢复。");
        showError("连接异常");
      });
    })
    .catch(function (err) {
      setTransportState("error");
      setConnectionBanner("error", err && err.message ? err.message : "恢复远端会话失败");
      debugRestoreLog("prepare_session_failed", { message: err && err.message ? err.message : String(err || "") });
      showError(err && err.message ? err.message : "恢复远端会话失败");
    });
}

async function checkAuth() {
  var res = await fetch("/api/auth", { credentials: "same-origin" });
  var data = await apiJSON(res);
  return Boolean(data.authenticated);
}

async function submitLogin(password) {
  var res = await fetch("/api/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    body: JSON.stringify({ password: password }),
  });
  if (!res.ok) {
    throw new Error("密码错误");
  }
}

async function logout() {
  await fetch("/api/logout", {
    method: "POST",
    credentials: "same-origin",
  }).catch(function () {});
  if (ws) {
    var current = ws;
    ws = null;
    wsIntentionalClose = true;
    current.close();
  }
  input.value = "";
  pendingImages.forEach(function (item) {
    URL.revokeObjectURL(item.url);
  });
  pendingImages = [];
  renderAttachmentTray();
  updateSendState();
  showLoginScreen();
}

function enterApp() {
  hideSessionChooser();
  hideLoginScreen();
  hideCodexAuthScreen();
  autoResize();
  renderAttachmentTray();
  updateSendState();
  ensureSocket();
}

async function openSessionChooser() {
  hideLoginScreen();
  showSessionChooser();
  var items = await fetchSessions().catch(function () { return null; });
  if (!items || !items.length) {
    resumeSessionChoice.disabled = true;
    resumeEmpty.hidden = false;
    return;
  }
  resumeSessionChoice.disabled = false;
  resumeSessionChoice.dataset.ready = "true";
  resumeSessionChoice.dataset.items = JSON.stringify(items);
}

async function boot() {
  var authenticated = await checkAuth().catch(function () { return false; });
  if (!authenticated) {
    showLoginScreen();
    return;
  }
  var items = await fetchSessions().catch(function () { return []; });
  var saved = String(currentSessionId || "").trim();
  var matched = saved ? items.find(function (item) { return item && item.id === saved; }) : null;
  if (matched) {
    setCurrentProvider(matched.provider || currentProvider);
    if (providerRequiresAuthFor(currentProvider)) {
      var codexAuth = await checkCodexAuthStatus().catch(function () { return { loggedIn: true }; });
      if (!codexAuth.loggedIn) {
        showCodexAuthScreen("当前机器上的 Codex 尚未授权，或授权已失效。");
        return;
      }
    }
    setSession(matched.id);
    enterApp();
    return;
  }
  var savedRef = getCurrentSessionRef() || findSessionRefByLocalSessionId(saved);
  if (savedRef) {
    try {
      if (providerRequiresAuthFor(savedRef.provider)) {
        var savedRefAuth = await checkCodexAuthStatus().catch(function () { return { loggedIn: true }; });
        if (!savedRefAuth.loggedIn) {
          showCodexAuthScreen("当前机器上的 Codex 尚未授权，或授权已失效。");
          return;
        }
      }
      await restoreSessionWithRetry(savedRef, false, { attempts: 4, delayMs: 1200 });
      enterApp();
      return;
    } catch (err) {
      setCurrentSessionRef(null);
      removeSessionRef(savedRef);
    }
  }
  if (saved) {
    clearStoredSession();
    currentSessionId = "";
  }
  await openSessionChooser();
}

async function resolveSessionId(prefix) {
  var query = String(prefix || "").trim();
  if (!query) {
    throw new Error("缺少会话 ID");
  }
  var items = await fetchSessions();
  var match = items.find(function (item) {
    return String(item.id || "").startsWith(query);
  });
  if (!match) {
    throw new Error("没有找到匹配的会话");
  }
  return match.id;
}

async function fetchSessions() {
  var res = await fetch("/api/sessions");
  var data = await apiJSON(res);
  var liveItems = (data.items || []).filter(function (item) {
    return item && item.id;
  });
  var liveRefs = {};
  liveItems.forEach(function (item) {
    var key = sessionRefIdentity(item);
    if (key) liveRefs[key] = true;
  });
  var storedItems = storedSessionList().filter(function (item) {
    return item && item.id && !liveRefs[sessionRefIdentity(item)];
  });
  return liveItems.concat(storedItems);
}

async function restoreSession(ref, connectNow) {
  var normalized = normalizeSessionRef(ref);
  if (!normalized) {
    debugRestoreLog("restore_rejected", { reason: "missing_ref", ref: ref || null });
    throw new Error("缺少可恢复的远端会话");
  }
  var cache = getSessionCache(normalized);
  debugRestoreLog("restore_request", {
    refId: normalized.refId,
    localSessionId: normalized.localSessionId,
    provider: normalized.provider,
    cachedMessages: cache && Array.isArray(cache.messages) ? cache.messages.length : 0,
    cachedEvents: cache && Array.isArray(cache.events) ? cache.events.length : 0
  });
  var res = await fetch("/api/session/restore", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      restoreRef: normalized,
      provider: normalized.provider,
      model: normalized.model,
      workdir: normalized.workdir,
      codexThreadId: normalized.codexThreadId,
      providerSessionId: normalized.providerSessionId,
      messages: normalized.provider === "claude" && cache && Array.isArray(cache.messages) ? cache.messages : [],
      events: normalized.provider === "claude" && cache && Array.isArray(cache.events) ? cache.events : [],
      draftMessage: normalized.provider === "claude" && cache && cache.draftMessage ? cache.draftMessage : null
    }),
  });
  var data = await apiJSON(res);
  debugRestoreLog("restore_response", { sessionId: data.sessionId, provider: normalized.provider });
  setCurrentProvider(normalized.provider || currentProvider);
  setCurrentSessionRef(normalized);
  setSession(data.sessionId);
  replaceTimeline([], []);
  setTaskState(false);
  setFooterStatus("ready", "已恢复远端会话");
  if (ws) {
    var current = ws;
    ws = null;
    wsIntentionalClose = true;
    current.close();
  }
  if (connectNow !== false) {
    ensureSocket();
  }
}

async function switchSession(sessionId, connectNow, providerID, sessionItem) {
  var nextId = String(sessionId || "").trim();
  if (!nextId) {
    throw new Error("缺少会话 ID");
  }
  if (sessionItem && sessionItem.restoreRef) {
    await restoreSession(sessionItem.restoreRef, connectNow);
    return;
  }
  if (nextId.indexOf("ref:") === 0) {
    var storedMatch = storedSessionList().find(function (item) {
      return item && item.id === nextId;
    });
    if (!storedMatch || !storedMatch.restoreRef) {
      throw new Error("没有找到可恢复的远端会话");
    }
    await restoreSession(storedMatch.restoreRef, connectNow);
    return;
  }
  if (nextId === currentSessionId) {
    input.value = "";
    autoResize();
    return;
  }
  removeWorkingPlaceholder();
  streamStates.forEach(function (state) {
    if (state && state.timer) {
      clearTimeout(state.timer);
    }
  });
  streamStates.clear();
  activeDraftId = "";
  pendingImages.forEach(function (item) {
    URL.revokeObjectURL(item.url);
  });
  pendingImages = [];
  renderAttachmentTray();
  renderEmpty();
  input.value = "";
  autoResize();
  var nextProvider = String(providerID || "").trim();
  if (!nextProvider) {
    var items = await fetchSessions().catch(function () { return []; });
    var matched = items.find(function (item) {
      return item && String(item.id || "").trim() === nextId;
    });
    nextProvider = matched && matched.provider ? String(matched.provider).trim() : "";
  }
  setCurrentProvider(nextProvider || currentProvider);
  setCurrentSessionRef(sessionItem || {
    restoreRef: sessionItem && sessionItem.restoreRef,
    provider: nextProvider || currentProvider,
    model: sessionItem && sessionItem.model,
    workdir: sessionItem && sessionItem.workdir,
    codexThreadId: sessionItem && sessionItem.codexThreadId,
    providerSessionId: sessionItem && sessionItem.providerSessionId,
    updatedAt: sessionItem && sessionItem.updatedAt
  });
  setTaskState(false);
  setSession(nextId);
  setFooterStatus("ready", "已恢复会话 " + shortSession(nextId));
  if (ws) {
    var current = ws;
    ws = null;
    wsIntentionalClose = true;
    current.close();
  }
  if (connectNow === false) {
    return;
  }
  ensureSocket();
}
