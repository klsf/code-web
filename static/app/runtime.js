function apiPayload(json) {
  if (!json || typeof json !== "object") return {};
  if (!Object.prototype.hasOwnProperty.call(json, "data")) return json;
  return json.data || {};
}

async function apiJSON(res) {
  var json = await res.json().catch(function () { return null; });
  if (!res.ok) {
    var message = json && json.message ? json.message : "请求失败";
    throw new Error(message || "请求失败");
  }
  return apiPayload(json);
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
    body: JSON.stringify({ password: password })
  });
  if (!res.ok) {
    throw new Error("密码错误");
  }
}

async function fetchSessions() {
  var res = await fetch("/api/sessions", { credentials: "same-origin" });
  var data = await apiJSON(res);
  return Array.isArray(data.items) ? data.items : [];
}

async function submitLogout() {
  var res = await fetch("/api/logout", {
    method: "POST",
    credentials: "same-origin"
  });
  if (!res.ok) {
    throw new Error("退出登录失败");
  }
}

async function deleteSession(item) {
  var res = await fetch("/api/session/delete", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      id: item && item.restoreRef ? "" : item.id,
      provider: item ? item.provider : "",
      restoreRef: item && item.restoreRef ? item.restoreRef : null
    })
  });
  if (!res.ok) {
    throw new Error(await res.text());
  }
  if (item && item.id && item.id === currentSessionId) {
    localStorage.removeItem("sessionId");
    currentSessionId = "";
  }
}

async function createSession(workdir, connectNow, providerID) {
  var nextProvider = String(providerID || currentProvider || "claude").trim().toLowerCase();
  var res = await fetch("/api/session/new", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ provider: nextProvider, workdir: String(workdir || "").trim() })
  });
  var data = await apiJSON(res);
  setCurrentProvider(nextProvider);
  setSession(data.sessionId || "");
  populateModelSelect(nextProvider);
  replaceTimeline([], [], null);
  setTaskState(false);
  closeSocket();
  if (connectNow !== false) {
    ensureSocket();
  }
  return data.sessionId || "";
}

async function restoreSession(item, connectNow) {
  var restoreRef = item && item.restoreRef ? item.restoreRef : {
    provider: item.provider,
    providerSessionId: item.providerSessionID || item.providerSessionId || item.id
  };
  var res = await fetch("/api/session/restore", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ provider: restoreRef.provider, sessionId: restoreRef.providerSessionId, restoreRef: restoreRef })
  });
  var data = await apiJSON(res);
  setCurrentProvider(restoreRef.provider || currentProvider);
  setSession(data.sessionId || "");
  populateModelSelect(restoreRef.provider || currentProvider, item && item.model);
  replaceTimeline([], [], null);
  setTaskState(false);
  closeSocket();
  if (connectNow !== false) {
    ensureSocket();
  }
}

function closeSocket() {
  clearTimeout(reconnectTimer);
  if (!ws) return;
  var current = ws;
  ws = null;
  wsIntentionalClose = true;
  try {
    current.close();
  } catch (err) {}
}

function ensureSocket() {
  clearTimeout(reconnectTimer);
  if (!currentSessionId) return;
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    return;
  }
  connect();
}

function connect() {
  clearTimeout(reconnectTimer);
  if (!currentSessionId) return;

  var protocol = location.protocol === "https:" ? "wss:" : "ws:";
  wsIntentionalClose = false;
  ws = new WebSocket(protocol + "//" + location.host + "/ws");
  var socket = ws;
  setTransportState("connecting");

  socket.addEventListener("open", function () {
    if (ws !== socket) return;
    setTransportState("connected");
    setConnectionBanner("connecting", "握手已建立，正在同步会话快照。");
    socket.send(JSON.stringify({ type: "hello", sessionId: currentSessionId }));
  });

  socket.addEventListener("message", function (evt) {
    if (ws !== socket) return;
    var data = JSON.parse(evt.data);
    if (data.type === "snapshot" && data.session) {
      setSession(data.session.id);
      setMeta({ provider: data.session.provider, model: data.session.model, cwd: data.session.workdir });
      replaceTimeline(data.session.messages || [], data.session.events || [], data.session.draftMessage || null);
      setTaskState(Boolean(data.running));
      setConnectionBanner("connected", "已连接到 " + shortSession(data.session.id) + "，实时消息同步正常。");
      return;
    }
    if (data.type === "message" && data.message) {
      renderMessage(data.message);
      return;
    }
    if (data.type === "message_delta" && data.message) {
      removeDraftMessages();
      renderMessage(data.message, { draft: true });
      setFooterStatus("working", providerDisplayName() + " 正在输出");
      return;
    }
    if (data.type === "message_final" && data.message) {
      removeDraftMessages(data.message.id);
      renderMessage(data.message);
      setFooterStatus("ready", "本轮回复已完成");
      return;
    }
    if (data.type === "log" && data.log) {
      renderEvent(data.log);
      return;
    }
    if (data.type === "task_status") {
      setTaskState(Boolean(data.running));
      return;
    }
    if (data.type === "error" && data.error) {
      setTaskState(false);
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
    reconnectTimer = setTimeout(connect, 1500);
  });

  socket.addEventListener("error", function () {
    if (ws !== socket) return;
    setTransportState("error");
    setConnectionBanner("error", "WebSocket 连接异常，稍后会继续尝试恢复。");
  });
}

async function sendPrompt() {
  var content = String(input.value || "").trim();
  var hasImages = pendingImages.length > 0;
  if ((!content && !hasImages) || !currentSessionId || isRunning) return;

  var formData = new FormData();
  formData.append("sessionId", currentSessionId);
  formData.append("content", content);
  formData.append("model", String(modelSelect && modelSelect.value || "").trim());
  pendingImages.forEach(function (item) {
    formData.append("images", item.file, item.file.name);
  });

  ensureSocket();
  setTaskState(true);
  setFooterStatus("working", compact(content || ("已附加 " + pendingImages.length + " 张图片")));

  try {
    var res = await fetch("/api/send", { method: "POST", credentials: "same-origin", body: formData });
    if (!res.ok) {
      throw new Error(await res.text());
    }
    input.value = "";
    pendingImages.forEach(function (item) {
      URL.revokeObjectURL(item.url);
    });
    pendingImages = [];
    renderAttachmentTray();
    autoResize();
    updateSendState();
  } catch (err) {
    setTaskState(false);
    showError(err && err.message ? err.message : "发送失败");
  }
}

async function switchProvider(providerID) {
  var nextProvider = String(providerID || "").trim().toLowerCase();
  if (!nextProvider || nextProvider === currentProvider) {
    syncProviderPicker();
    syncChooserProviderPicker();
    return;
  }
  if (isRunning) {
    syncProviderPicker();
    syncChooserProviderPicker();
    showError("当前会话仍在生成中，请稍候切换");
    return;
  }

  setCurrentProvider(nextProvider);
  if (!sessionChooser.hidden) {
    return;
  }

  closeSocket();
  setTransportState("connecting");
  renderEmpty();

  try {
    await createSession(statusCwd.textContent || workdirInput.value, true, nextProvider);
    setFooterStatus("ready", "已切换到 " + providerDisplayName(nextProvider));
  } catch (err) {
    showError(err && err.message ? err.message : "切换会话失败");
  }
}

function enterApp() {
  hideLoginScreen();
  hideSessionChooser();
  autoResize();
  updateSendState();
  ensureSocket();
}

async function backToSessionChooser() {
  if (isRunning) {
    showError("当前会话仍在生成中，请稍候返回");
    return;
  }
  closeSocket();
  await openSessionChooser();
}

async function quickCreateSession() {
  if (isRunning) {
    showError("当前会话仍在生成中，请稍候新建");
    return;
  }
  hideSessionModal();
  await backToSessionChooser();
}

async function openSessionChooser() {
  hideLoginScreen();
  showSessionChooser();
  var items = await fetchSessions().catch(function () { return []; });
  resumeSessionChoice.dataset.items = JSON.stringify(items);
  resumeSessionChoice.disabled = items.length === 0;
  resumeEmpty.hidden = items.length > 0;
}

async function openSessionModal() {
  var items = await fetchSessions().catch(function () { return []; });
  resumeEmpty.hidden = items.length > 0;
  if (!items.length) {
    showError("没有可恢复的历史会话");
    return;
  }
  renderResumeList(items, {
    onOpen: async function (item) {
      hideSessionModal();
      closeSocket();
      replaceTimeline([], [], null);
      setTaskState(false);
      if (item && item.restoreRef) {
        await restoreSession(item, false);
      } else {
        setCurrentProvider(item.provider || currentProvider);
        setSession(item.id);
        populateModelSelect(item.provider || currentProvider, item.model);
      }
      enterApp();
    },
    onDelete: async function (item, row) {
      await deleteSession(item);
      row.remove();
      if (!resumeList.children.length) {
        hideSessionModal();
      }
    }
  });
  showSessionModal();
}

async function logoutAndReset() {
  closeSocket();
  try {
    await submitLogout();
  } catch (err) {
    showError(err && err.message ? err.message : "退出登录失败");
    return;
  }

  localStorage.removeItem("sessionId");
  currentSessionId = "";
  pendingImages.forEach(function (item) {
    URL.revokeObjectURL(item.url);
  });
  pendingImages = [];
  renderAttachmentTray();
  input.value = "";
  autoResize();
  updateSendState();
  replaceTimeline([], [], null);
  setTaskState(false);
  setTransportState("connecting");
  setSession("");
  setMeta({
    provider: appConfig().provider || currentProvider,
    model: appConfig().model || "unknown",
    cwd: "/"
  });
  hideSessionModal();
  hideActionModal();
  showLoginScreen();
}

async function tryRestoreSavedSession() {
  var saved = String(currentSessionId || "").trim();
  if (!saved) {
    return false;
  }
  var items = await fetchSessions().catch(function () { return []; });
  var liveMatch = items.find(function (item) { return item && String(item.id || "") === saved && !item.restoreRef; });
  if (liveMatch) {
    setCurrentProvider(liveMatch.provider || currentProvider);
    setSession(liveMatch.id);
    enterApp();
    return true;
  }
  return false;
}

async function boot() {
  applyBuildInfo();
  populateModelSelect(currentProvider, appConfig().model);
  autoResize();
  renderAttachmentTray();
  updateSendState();
  renderEmpty();
  if (workdirInput) {
    workdirInput.value = statusCwd.textContent && statusCwd.textContent !== "/" ? statusCwd.textContent : "H:\\go\\code-web-new";
  }

  var authenticated = await checkAuth().catch(function () { return false; });
  if (!authenticated) {
    showLoginScreen();
    return;
  }

  hideLoginScreen();
  if (await tryRestoreSavedSession()) {
    return;
  }
  await openSessionChooser();
}
