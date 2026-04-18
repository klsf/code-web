import { computed, onBeforeUnmount, onMounted, reactive, watch } from "vue";
import {
  compact,
  eventMeta,
  eventSummaryText,
  eventVariant,
  formatTime,
  providerIconPaths,
  shortSession,
  shouldHideEvent,
  transportCopy
} from "../lib/chat-helpers.js";

export function useChatApp() {
  const state = reactive({
    appName: "Code Web",
    version: "vdev",
    providers: [],
    currentProvider: "claude",
    currentSessionId: localStorage.getItem("sessionId") || "",
    currentModel: "unknown",
    currentCwd: "/",
    authenticated: false,
    screen: "login",
    showSessionModal: false,
    showActionModal: false,
    loginPassword: "",
    loginError: "",
    chooserWorkdir: "H:\\go\\code-web-new",
    sessions: [],
    running: false,
    transportState: "connecting",
    footerState: "Ready",
    footerDetail: "等待输入",
    messages: [],
    events: [],
    draftMessage: null,
    input: "",
    pendingImages: [],
    errorToast: "",
    timelineStamp: 0
  });

  const eventGroupUi = reactive({});
  let ws = null;
  let reconnectTimer = null;
  let wsIntentionalClose = false;
  let errorToastTimer = null;

  function scrollTimelineToBottom() {
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        const timelineNode = document.querySelector(".timeline-panel");
        if (timelineNode && timelineNode.scrollHeight > timelineNode.clientHeight) {
          timelineNode.scrollTop = timelineNode.scrollHeight;
        }
        const root = document.scrollingElement || document.documentElement || document.body;
        if (root) root.scrollTop = root.scrollHeight;
        window.scrollTo(0, document.body.scrollHeight);
      });
    });
  }

  function appConfig() {
    return window.__APP_CONFIG || {};
  }

  function availableProviders() {
    return Array.isArray(state.providers) ? state.providers : [];
  }

  function providerDisplayName(providerID = state.currentProvider) {
    const current = String(providerID || "").trim().toLowerCase();
    const matched = availableProviders().find((item) => String(item?.id || "").trim().toLowerCase() === current);
    return matched?.name || matched?.displayName || current || "Claude";
  }

  function providerModels(providerID = state.currentProvider) {
    return availableProviders().find((item) => item?.id === providerID)?.models || [];
  }

  function ensureEventGroupUi(groupId) {
    if (!eventGroupUi[groupId]) {
      eventGroupUi[groupId] = { expanded: false, selectedEventId: null };
    }
    return eventGroupUi[groupId];
  }

  const selectedProvider = computed(() => providerDisplayName());
  const sessionLabel = computed(() => shortSession(state.currentSessionId));
  const modelOptions = computed(() => {
    const models = providerModels(state.currentProvider).slice();
    if (state.currentModel && !models.includes(state.currentModel)) models.push(state.currentModel);
    return models.length ? models : [appConfig().model || "unknown"];
  });
  const connectionInfo = computed(() => transportCopy[state.transportState] || transportCopy.connected);
  const chooserSessions = computed(() => state.sessions.filter(Boolean));
  const showConnectionBanner = computed(() => state.transportState !== "connected");
  const canSend = computed(() => !state.running && (state.input.trim().length > 0 || state.pendingImages.length > 0));

  function timelineItemRank(item) {
    if (item.kind === "event") return 1;
    const role = String(item.value?.role || "").toLowerCase();
    if (role === "user") return 0;
    if (role === "assistant") return 2;
    return 3;
  }

  const mergedTimeline = computed(() => {
    const items = [];
    state.messages.forEach((message, index) => {
      items.push({ kind: "message", createdAt: message.createdAt, value: message, order: index });
    });
    state.events.filter((event) => !shouldHideEvent(event)).forEach((event, index) => {
      items.push({ kind: "event", createdAt: event.createdAt, value: event, order: state.messages.length + index });
    });
    items.sort((left, right) => {
      const leftTime = new Date(left.createdAt).getTime();
      const rightTime = new Date(right.createdAt).getTime();
      if (leftTime !== rightTime) return leftTime - rightTime;
      const leftRank = timelineItemRank(left);
      const rightRank = timelineItemRank(right);
      if (leftRank !== rightRank) return leftRank - rightRank;
      return left.order - right.order;
    });

    const grouped = [];
    let currentEventGroup = null;
    items.forEach((item) => {
      if (item.kind !== "event") {
        currentEventGroup = null;
        grouped.push(item);
        return;
      }
      if (!currentEventGroup) {
        currentEventGroup = {
          kind: "event-group",
          id: `event-group-${item.value.id || item.order}`,
          createdAt: item.createdAt,
          events: []
        };
        grouped.push(currentEventGroup);
      }
      currentEventGroup.events.push({
        ...item.value,
        variant: eventVariant(item.value),
        summary: eventSummaryText(item.value),
        meta: eventMeta(item.value)
      });
    });

    if (state.draftMessage) {
      grouped.push({ kind: "draft", value: state.draftMessage, id: `draft-${state.draftMessage.id || "current"}` });
    }
    return grouped;
  });

  watch(
    () => state.currentSessionId,
    (value) => {
      if (value) localStorage.setItem("sessionId", value);
      else localStorage.removeItem("sessionId");
    }
  );

  watch(
    () => `${mergedTimeline.value.length}:${state.timelineStamp}`,
    () => {
      scrollTimelineToBottom();
    }
  );

  watch(
    () => state.screen,
    (value) => {
      if (value === "chat") scrollTimelineToBottom();
    }
  );

  watch(
    () => state.input,
    () => {
      requestAnimationFrame(() => {
        const promptNode = document.querySelector(".prompt-input");
        if (!promptNode) return;
        promptNode.style.height = "auto";
        promptNode.style.height = `${Math.min(promptNode.scrollHeight, 220)}px`;
      });
    }
  );

  function setFooterStatus(label, detail) {
    state.footerState = label;
    state.footerDetail = detail;
  }

  function setTransportState(nextState) {
    state.transportState = nextState;
    if (!state.running) {
      if (nextState === "connected") setFooterStatus("Ready", "等待输入");
      else if (nextState === "connecting") setFooterStatus("Connecting", "正在建立连接");
      else if (nextState === "reconnecting") setFooterStatus("Retrying", "正在恢复连接");
      else setFooterStatus("Offline", "连接不可用");
    }
  }

  function setTaskState(running) {
    const wasRunning = state.running;
    state.running = Boolean(running);
    if (state.running) setFooterStatus("Working", `${selectedProvider.value} 正在生成`);
    else if (state.transportState === "connected") setFooterStatus("Ready", "等待输入");
    if (wasRunning && !state.running) scrollTimelineToBottom();
  }

  function setSession(id) {
    state.currentSessionId = String(id || "");
  }

  function setMeta(meta = {}) {
    if (meta.provider) state.currentProvider = meta.provider;
    if (meta.model) state.currentModel = meta.model;
    if (meta.cwd) state.currentCwd = meta.cwd;
  }

  function replaceTimeline(messages = [], events = [], draftMessage = null) {
    state.messages = messages;
    state.events = events;
    state.draftMessage = draftMessage;
    state.timelineStamp += 1;
  }

  function showError(message) {
    state.errorToast = compact(message || "操作失败");
    setFooterStatus("Error", state.errorToast);
    clearTimeout(errorToastTimer);
    errorToastTimer = setTimeout(() => {
      state.errorToast = "";
    }, 3200);
  }

  function apiPayload(json) {
    if (!json || typeof json !== "object") return {};
    return Object.prototype.hasOwnProperty.call(json, "data") ? json.data || {} : json;
  }

  async function apiJSON(res) {
    const json = await res.json().catch(() => null);
    if (!res.ok) throw new Error(json?.message || "请求失败");
    return apiPayload(json);
  }

  async function checkAuth() {
    const res = await fetch("/api/auth", { credentials: "same-origin" });
    const data = await apiJSON(res);
    return Boolean(data.authenticated);
  }

  async function submitLogin() {
    const res = await fetch("/api/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify({ password: state.loginPassword })
    });
    if (!res.ok) throw new Error("密码错误");
  }

  async function fetchSessions() {
    const res = await fetch("/api/sessions", { credentials: "same-origin" });
    const data = await apiJSON(res);
    return Array.isArray(data.items) ? data.items : [];
  }

  async function refreshSessions() {
    state.sessions = await fetchSessions().catch(() => []);
  }

  async function createSession(connectNow = true) {
    const res = await fetch("/api/session/new", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        provider: state.currentProvider,
        workdir: String(state.chooserWorkdir || "").trim()
      })
    });
    const data = await apiJSON(res);
    setSession(data.sessionId || "");
    state.currentModel = providerModels(state.currentProvider)[0] || appConfig().model || "unknown";
    replaceTimeline([], [], null);
    setTaskState(false);
    closeSocket();
    if (connectNow) ensureSocket();
  }

  async function restoreSession(item, connectNow = true) {
    const restoreRef = item?.restoreRef
      ? item.restoreRef
      : { provider: item.provider, providerSessionId: item.providerSessionID || item.providerSessionId || item.id };
    const res = await fetch("/api/session/restore", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        provider: restoreRef.provider,
        sessionId: restoreRef.providerSessionId,
        restoreRef
      })
    });
    const data = await apiJSON(res);
    state.currentProvider = restoreRef.provider || state.currentProvider;
    setSession(data.sessionId || "");
    state.currentModel = item?.model || providerModels(state.currentProvider)[0] || appConfig().model || "unknown";
    replaceTimeline([], [], null);
    setTaskState(false);
    closeSocket();
    if (connectNow) ensureSocket();
  }

  async function deleteSession(item) {
    const res = await fetch("/api/session/delete", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        id: item?.restoreRef ? "" : item?.id,
        provider: item?.provider || "",
        restoreRef: item?.restoreRef || null
      })
    });
    if (!res.ok) throw new Error(await res.text());
    if (item?.id && item.id === state.currentSessionId) setSession("");
  }

  async function submitLogout() {
    const res = await fetch("/api/logout", { method: "POST", credentials: "same-origin" });
    if (!res.ok) throw new Error("退出登录失败");
  }

  function closeSocket() {
    clearTimeout(reconnectTimer);
    if (!ws) return;
    const current = ws;
    ws = null;
    wsIntentionalClose = true;
    try { current.close(); } catch {}
  }

  function ensureSocket() {
    clearTimeout(reconnectTimer);
    if (!state.currentSessionId) return;
    if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return;
    connect();
  }

  function connect() {
    clearTimeout(reconnectTimer);
    if (!state.currentSessionId) return;
    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    wsIntentionalClose = false;
    ws = new WebSocket(`${protocol}//${location.host}/ws`);
    const socket = ws;
    setTransportState("connecting");

    socket.addEventListener("open", () => {
      if (ws !== socket) return;
      setTransportState("connected");
      socket.send(JSON.stringify({ type: "hello", sessionId: state.currentSessionId }));
    });

    socket.addEventListener("message", (evt) => {
      if (ws !== socket) return;
      const data = JSON.parse(evt.data);
      if (data.type === "snapshot" && data.session) {
        setSession(data.session.id);
        setMeta({ provider: data.session.provider, model: data.session.model, cwd: data.session.workdir });
        replaceTimeline(data.session.messages || [], data.session.events || [], data.session.draftMessage || null);
        setTaskState(Boolean(data.running));
        scrollTimelineToBottom();
        return;
      }
      if (data.type === "message" && data.message) {
        state.messages = [...state.messages, data.message];
        state.timelineStamp += 1;
        return;
      }
      if (data.type === "message_delta" && data.message) {
        state.draftMessage = data.message;
        setFooterStatus("Working", `${selectedProvider.value} 正在输出`);
        state.timelineStamp += 1;
        return;
      }
      if (data.type === "message_final" && data.message) {
        state.draftMessage = null;
        state.messages = state.messages.filter((item) => item.id !== data.message.id).concat(data.message);
        setFooterStatus("Ready", "本轮回复已完成");
        state.timelineStamp += 1;
        scrollTimelineToBottom();
        return;
      }
      if (data.type === "log" && data.log) {
        state.events = [...state.events, data.log];
        state.timelineStamp += 1;
        return;
      }
      if (data.type === "task_status") {
        setTaskState(Boolean(data.running));
        return;
      }
      if (data.type === "error" && data.error) {
        setTaskState(false);
        showError(data.error);
        scrollTimelineToBottom();
      }
    });

    socket.addEventListener("close", () => {
      if (ws === socket) ws = null;
      if (wsIntentionalClose) {
        wsIntentionalClose = false;
        return;
      }
      setTransportState("reconnecting");
      reconnectTimer = setTimeout(connect, 1500);
    });

    socket.addEventListener("error", () => {
      if (ws !== socket) return;
      setTransportState("error");
    });
  }

  function revokePendingImages() {
    state.pendingImages.forEach((item) => URL.revokeObjectURL(item.url));
    state.pendingImages = [];
  }

  function addPendingImageFiles(files) {
    const next = [];
    Array.from(files || []).forEach((file) => {
      if (!String(file?.type || "").toLowerCase().startsWith("image/")) return;
      next.push({ file, url: URL.createObjectURL(file) });
    });
    if (!next.length) return false;
    state.pendingImages = state.pendingImages.concat(next);
    return true;
  }

  async function sendPrompt() {
    const content = state.input.trim();
    if ((!content && !state.pendingImages.length) || !state.currentSessionId || state.running) return;
    const formData = new FormData();
    formData.append("sessionId", state.currentSessionId);
    formData.append("content", content);
    formData.append("model", state.currentModel);
    state.pendingImages.forEach((item) => formData.append("images", item.file, item.file.name));

    ensureSocket();
    setTaskState(true);
    setFooterStatus("Working", compact(content || `已附加 ${state.pendingImages.length} 张图片`));

    try {
      const res = await fetch("/api/send", { method: "POST", credentials: "same-origin", body: formData });
      if (!res.ok) throw new Error(await res.text());
      state.input = "";
      revokePendingImages();
    } catch (error) {
      setTaskState(false);
      showError(error?.message || "发送失败");
    }
  }

  function groupPreviewIcons(group) {
    return group.events.slice(0, 8);
  }

  function isGroupExpanded(group) {
    return ensureEventGroupUi(group.id).expanded;
  }

  function selectedEventId(group) {
    return ensureEventGroupUi(group.id).selectedEventId;
  }

  function toggleGroupExpanded(group) {
    const ui = ensureEventGroupUi(group.id);
    ui.expanded = !ui.expanded;
    if (!ui.expanded) ui.selectedEventId = null;
  }

  function toggleEventDetail(group, eventId) {
    const ui = ensureEventGroupUi(group.id);
    ui.expanded = true;
    ui.selectedEventId = ui.selectedEventId === eventId ? null : eventId;
  }

  function actionLabel(group) {
    return `${group.events.length} actions`;
  }

  function eventRowLabel(event) {
    return event.summary || event.title || "action";
  }

  async function handleLogin() {
    state.loginError = "";
    try {
      await submitLogin();
      state.authenticated = true;
      state.screen = "chooser";
      await refreshSessions();
    } catch (error) {
      state.loginError = error?.message || "登录失败";
      showError(state.loginError);
    }
  }

  async function handleCreateSession() {
    try {
      await createSession(true);
      state.screen = "chat";
    } catch (error) {
      showError(error?.message || "新建会话失败");
    }
  }

  async function openSessionModal() {
    await refreshSessions();
    if (!state.sessions.length) {
      showError("没有可恢复的历史会话");
      return;
    }
    state.showSessionModal = true;
  }

  async function openChooser() {
    if (state.running) {
      showError("当前会话仍在生成中，请稍候返回");
      return;
    }
    closeSocket();
    state.screen = "chooser";
    await refreshSessions();
  }

  async function handleOpenSession(item) {
    state.showSessionModal = false;
    closeSocket();
    replaceTimeline([], [], null);
    setTaskState(false);
    if (item?.restoreRef) await restoreSession(item, false);
    else {
      state.currentProvider = item.provider || state.currentProvider;
      setSession(item.id);
      state.currentModel = item.model || modelOptions.value[0];
    }
    state.screen = "chat";
    ensureSocket();
  }

  async function handleDeleteSession(item) {
    try {
      await deleteSession(item);
      await refreshSessions();
      if (!state.sessions.length) state.showSessionModal = false;
    } catch (error) {
      showError(error?.message || "删除失败");
    }
  }

  async function handleLogout() {
    closeSocket();
    try {
      await submitLogout();
    } catch (error) {
      showError(error?.message || "退出登录失败");
      return;
    }
    setSession("");
    setMeta({ provider: appConfig().provider || "claude", model: appConfig().model || "unknown", cwd: "/" });
    revokePendingImages();
    replaceTimeline([], [], null);
    state.input = "";
    state.loginPassword = "";
    state.loginError = "";
    state.screen = "login";
    state.authenticated = false;
    state.showSessionModal = false;
    state.showActionModal = false;
    setTaskState(false);
    setTransportState("connecting");
  }

  async function tryRestoreSavedSession() {
    if (!state.currentSessionId) return false;
    const items = await fetchSessions().catch(() => []);
    state.sessions = items;
    const liveMatch = items.find((item) => item && String(item.id || "") === state.currentSessionId && !item.restoreRef);
    if (!liveMatch) return false;
    state.currentProvider = liveMatch.provider || state.currentProvider;
    state.currentModel = liveMatch.model || state.currentModel;
    state.screen = "chat";
    ensureSocket();
    return true;
  }

  function handlePaste(evt) {
    const clipboard = evt.clipboardData;
    if (!clipboard?.items?.length) return;
    const files = [];
    Array.from(clipboard.items).forEach((item) => {
      if (item.kind !== "file") return;
      const file = item.getAsFile();
      if (file) files.push(file);
    });
    if (files.length && addPendingImageFiles(files)) evt.preventDefault();
  }

  function handleImageSelection(evt) {
    addPendingImageFiles(evt.target.files || []);
    evt.target.value = "";
  }

  function removePendingImage(index) {
    const [item] = state.pendingImages.splice(index, 1);
    if (item?.url) URL.revokeObjectURL(item.url);
  }

  function resumeBadge(item) {
    return item?.running ? "运行中" : "空闲";
  }

  onMounted(async () => {
    const cfg = appConfig();
    state.providers = Array.isArray(cfg.providers) ? cfg.providers : [];
    state.currentProvider = cfg.provider || state.currentProvider;
    state.currentModel = cfg.model || state.currentModel;
    state.appName = cfg.appName || state.appName;
    state.version = String(cfg.version || "dev").startsWith("v") ? String(cfg.version || "dev") : `v${cfg.version || "dev"}`;
    document.title = state.appName;

    const authenticated = await checkAuth().catch(() => false);
    if (!authenticated) {
      state.screen = "login";
      return;
    }

    state.authenticated = true;
    if (await tryRestoreSavedSession()) return;
    state.screen = "chooser";
    await refreshSessions();
  });

  onBeforeUnmount(() => {
    closeSocket();
    revokePendingImages();
    clearTimeout(errorToastTimer);
  });

  return {
    state,
    selectedProvider,
    sessionLabel,
    modelOptions,
    connectionInfo,
    chooserSessions,
    showConnectionBanner,
    canSend,
    mergedTimeline,
    formatTime,
    providerIconPaths,
    actionLabel,
    groupPreviewIcons,
    isGroupExpanded,
    selectedEventId,
    toggleGroupExpanded,
    toggleEventDetail,
    eventRowLabel,
    handleLogin,
    handleCreateSession,
    openSessionModal,
    openChooser,
    handleOpenSession,
    handleDeleteSession,
    handleLogout,
    sendPrompt,
    handlePaste,
    handleImageSelection,
    removePendingImage,
    resumeBadge
  };
}
