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
  globe: ["M12 3c4.971 0 9 4.029 9 9s-4.029 9-9 9-9-4.029-9-9 4.029-9 9-9Z", "M3 12h18", "M12 3a14.5 14.5 0 0 1 0 18", "M12 3a14.5 14.5 0 0 0 0 18"],
  browser: ["M4 6.5h16", "M7 4.5h10a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2v-11a2 2 0 0 1 2-2Z", "M8 9.5h3", "M13 9.5h3"],
  pointer: ["M7 4.5 17.5 15 12.8 15.4 15 20 12.7 21 10.5 16.3 7 19.5Z"],
  form: ["M6 7.5h12", "M6 12h12", "M6 16.5h7"],
  code: ["M9 8 5 12l4 4", "M15 8l4 4-4 4", "M13 6 11 18"],
  image: ["M5 7.5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2v-9Z", "M8.5 10.5h.01", "M7.5 16l3.2-3.2 2.3 2.3 1.8-1.8L18 16"],
  network: ["M5 12h4", "M15 12h4", "M9 12a3 3 0 1 0 6 0a3 3 0 1 0-6 0Z"],
  plan: ["M7 7.5h10", "M7 12h10", "M7 16.5h6", "M5 7.5h.01", "M5 12h.01", "M5 16.5h.01"]
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
  const token = `${normalizeEventToken(event?.kind)} ${normalizeEventToken(event?.stepType)} ${normalizeEventToken(event?.title)} ${normalizeEventToken(event?.target)}`;
  if (token.includes("updateplan") || token.includes("todo") || token.includes("plan")) return "plan";
  if (token.includes("search") || token.includes("find") || token.includes("grep")) return "search";
  if (token.includes("file") || token.includes("patch") || token.includes("diff")) return "file";
  if (token.includes("screenshot") || token.includes("snapshot") || token.includes("image")) return "image";
  if (token.includes("evaluatescript") || token.includes("script") || token.includes("debug") || token.includes("breakpoint")) return "code";
  if (token.includes("network") || token.includes("request") || token.includes("xhr") || token.includes("websocket")) return "network";
  if (token.includes("click") || token.includes("presskey") || token.includes("hover") || token.includes("drag")) return "pointer";
  if (token.includes("fill") || token.includes("type") || token.includes("selectoption") || token.includes("upload")) return "form";
  if (token.includes("page") || token.includes("browser") || token.includes("navigate")) return "browser";
  if (token.includes("http") || token.includes("web") || token.includes("url") || token.includes("open")) return "globe";
  if (token.includes("tool")) return "tool";
  if (normalizeEventToken(event?.kind) === "command") return "command";
  return "status";
}

function parseJSONSafely(text) {
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

function firstInterestingValue(value) {
  if (typeof value === "string") {
    const text = value.trim();
    if (!text) return "";
    return text;
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      const found = firstInterestingValue(item);
      if (found) return found;
    }
    return "";
  }
  if (value && typeof value === "object") {
    const preferredKeys = [
      "url", "uri", "href", "link", "page", "website",
      "path", "file", "filename", "file_path", "filepath",
      "command", "cmd", "query", "q", "pattern", "name", "title"
    ];
    for (const key of preferredKeys) {
      if (key in value) {
        const found = firstInterestingValue(value[key]);
        if (found) return found;
      }
    }
    for (const item of Object.values(value)) {
      const found = firstInterestingValue(item);
      if (found) return found;
    }
  }
  return "";
}

function extractDetailFromBody(body) {
  const text = String(body || "").trim();
  if (!text) return "";

  const parsed = parseJSONSafely(text);
  if (parsed) {
    return firstInterestingValue(parsed);
  }

  const urlMatch = text.match(/https?:\/\/\S+/i);
  if (urlMatch) return urlMatch[0];

  const windowsPathMatch = text.match(/[A-Za-z]:\\[^\s"']+/);
  if (windowsPathMatch) return windowsPathMatch[0];

  const unixPathMatch = text.match(/\/[A-Za-z0-9._~\-\\/]+/);
  if (unixPathMatch) return unixPathMatch[0];

  return compact(text, 80);
}

function isGenericEventTarget(value) {
  const token = normalizeEventToken(value);
  return [
    "websearch", "openurl", "fetchurl", "tool", "tooluse", "toolcall", "functioncall",
    "readfile", "writefile", "patchfile", "applypatch", "shellcommand", "execcommand",
    "grep", "glob", "searchfiles", "findfiles"
  ].includes(token);
}

export function eventSummaryText(event) {
  const parts = [];
  const title = eventTitleText(event);
  const target = String(event?.target || "").trim();
  const body = String(reasoningBodyText(event) || "").trim();
  const detail = !isReasoningEvent(event)
    ? (!isGenericEventTarget(target) ? firstInterestingValue(target) : "") || extractDetailFromBody(body)
    : "";
  if (title) parts.push(title);
  if (detail && !isReasoningEvent(event) && normalizeEventToken(detail) !== normalizeEventToken(title)) parts.push(detail);
  else if (body) parts.push(body);
  return compact(parts.join(" · "));
}

export function eventDetailBody(event) {
  const blocks = [];
  if (event.target && !isReasoningEvent(event)) blocks.push(event.target);
  if (event.body) blocks.push(reasoningBodyText(event));
  return blocks.join("\n\n").trim();
}

export function eventDetailSections(event) {
  const sections = [];
  const target = String(event?.target || "").trim();
  const body = String(reasoningBodyText(event) || "").trim();
  const splitToken = /\n{2,}结果:\n/;

  if (target && !isReasoningEvent(event)) {
    sections.push({ label: "输入", value: target, tone: "input" });
  }

  if (body) {
    const parts = body.split(splitToken);
    if (parts[0]?.trim()) {
      sections.push({
        label: sections.length ? "补充" : "输入",
        value: parts[0].trim(),
        tone: sections.length ? "neutral" : "input"
      });
    }
    if (parts[1]?.trim()) {
      sections.push({ label: "结果", value: parts.slice(1).join("\n\n结果:\n").trim(), tone: "result" });
    }
  }

  if (!sections.length) {
    const fallback = eventDetailBody(event) || event?.summary || "";
    if (String(fallback).trim()) {
      sections.push({ label: "详情", value: String(fallback).trim(), tone: "neutral" });
    }
  }
  return sections;
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
