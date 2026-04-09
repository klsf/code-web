import MarkdownIt from "markdown-it";
import DOMPurify from "dompurify";

const md = new MarkdownIt({ breaks: true, linkify: true });

export const transportCopy = {
  connected: { badge: "live", title: "连接正常", detail: "实时消息会显示在这里。" },
  connecting: { badge: "link", title: "正在建立连接", detail: "正在与当前会话建立实时通道。" },
  reconnecting: { badge: "retry", title: "正在恢复连接", detail: "实时连接已断开，系统会自动重试。" },
  error: { badge: "error", title: "连接异常", detail: "实时通道暂不可用，可稍后重试。" }
};

export const iconMap = {
  command: ["M4 7h16", "M4 12h10", "M4 17h13"],
  reasoning: ["M12 3a9 9 0 1 0 9 9", "M12 7v5", "M12 16h.01"],
  status: ["M5 12.5 9 16.5 19 6.5"],
  search: ["M11 5a6 6 0 1 0 0 12a6 6 0 0 0 0-12", "m20 20-3.5-3.5"],
  file: ["M8 3.5h6l4.5 4.5V20a1 1 0 0 1-1 1H8a1 1 0 0 1-1-1V4.5a1 1 0 0 1 1-1Z", "M14 3.5V8h4.5"],
  tool: ["M14.5 6.5a2.5 2.5 0 1 1 3 3L10 17l-4 1 1-4 7.5-7.5Z"],
  globe: ["M12 3c4.971 0 9 4.029 9 9s-4.029 9-9 9-9-4.029-9-9 4.029-9 9-9Z", "M3 12h18", "M12 3a14.5 14.5 0 0 1 0 18", "M12 3a14.5 14.5 0 0 0 0 18"]
};

export function compact(text, limit = 120) {
  return String(text || "").replace(/\s+/g, " ").trim().slice(0, limit) || "等待输入";
}

export function formatTime(value) {
  if (!value) return "--:--";
  return new Date(value).toLocaleString("zh-CN", {
    hour12: false,
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit"
  });
}

export function shortSession(id) {
  return id ? String(id).slice(0, 8) : "unknown";
}

export function normalizeEventToken(value) {
  return String(value || "").trim().toLowerCase().replace(/[\s_-]+/g, "");
}

export function isReasoningEvent(event) {
  const stepType = normalizeEventToken(event?.stepType);
  const title = normalizeEventToken(event?.title);
  const body = normalizeEventToken(event?.body);
  return stepType === "reasoning" || title === "reasoning" || body.startsWith("reasoning");
}

export function shouldHideEvent(event) {
  if (!event) return true;
  const stepType = normalizeEventToken(event.stepType);
  const title = normalizeEventToken(event.title);
  const target = normalizeEventToken(event.target);
  if (stepType === "result") return true;
  if (stepType === "thread" || stepType === "turn" || title === "thread" || title === "turn") return true;
  if (target === "thread" || target === "turn") return true;
  return !String(event.title || event.body || event.target || "").trim();
}

export function eventTitleText(event) {
  if (isReasoningEvent(event)) return "思考中";
  return String(event?.title || "").trim() || "事件";
}

export function reasoningBodyText(event) {
  let text = String(event?.body || "").trim();
  if (!isReasoningEvent(event)) return text;
  text = text.replace(/^reasoning\s*:?\s*/i, "").trim();
  if (text.length > 160) {
    text = `${text.slice(0, 160).trim()}...`;
  }
  return text || "正在整理思路与下一步动作";
}

export function eventMeta(event) {
  const parts = [];
  if (isReasoningEvent(event)) parts.push("reasoning");
  else {
    if (event?.kind) parts.push(String(event.kind));
    if (event?.phase && event.phase !== "started") parts.push(String(event.phase));
  }
  parts.push(formatTime(event?.createdAt));
  return parts.join(" · ");
}

export function eventVariant(event) {
  if (isReasoningEvent(event)) return "reasoning";
  const token = `${normalizeEventToken(event?.kind)} ${normalizeEventToken(event?.title)} ${normalizeEventToken(event?.target)}`;
  if (token.includes("search") || token.includes("find") || token.includes("grep")) return "search";
  if (token.includes("file") || token.includes("patch") || token.includes("diff")) return "file";
  if (token.includes("http") || token.includes("web") || token.includes("url") || token.includes("open")) return "globe";
  if (token.includes("tool")) return "tool";
  if (normalizeEventToken(event?.kind) === "command") return "command";
  return "status";
}

export function eventSummaryText(event) {
  const parts = [];
  const title = eventTitleText(event);
  const target = String(event?.target || "").trim();
  const body = String(reasoningBodyText(event) || "").trim();
  if (title) parts.push(title);
  if (target && !isReasoningEvent(event)) parts.push(target);
  else if (body) parts.push(body);
  return compact(parts.join(" · "));
}

export function eventDetailBody(event) {
  const blocks = [];
  if (event.target && !isReasoningEvent(event)) blocks.push(event.target);
  if (event.body) blocks.push(reasoningBodyText(event));
  return blocks.join("\n\n").trim();
}

export function markdownHtml(text) {
  return DOMPurify.sanitize(md.render(String(text || "")));
}

export function previewImages(item) {
  return Array.isArray(item?.imageUrls) ? item.imageUrls : [];
}

export function providerIconPaths(id) {
  if (id === "claude") {
    return ["M24 5v7M24 36v7M5 24h7M36 24h7M11 11l5 5M32 32l5 5M11 37l5-5M32 16l5-5", "M24 14a10 10 0 1 0 0 20a10 10 0 0 0 0-20Z"];
  }
  return ["M34 12c-2.5-3-6-4.5-10.5-4.5C14.4 7.5 8 13.7 8 24s6.4 16.5 15.5 16.5c4.5 0 8-1.5 10.5-4.5", "M30 16h8v16h-8", "M22 16a8 8 0 1 0 0 16"];
}
