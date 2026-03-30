(function () {
  var defaultAuthGuideSteps = [
    "点击下面按钮，会在新页面打开 ChatGPT 登录授权。",
    "在新页面完成授权，浏览器会跳转到一个 http://localhost:1455/auth/callback?... 链接。",
    "复制完整回调链接，回到当前页面粘贴，然后点击“完成授权”。",
  ];

  var pollTimer = null;
  var currentSessionId = "";

  function authGuideSteps() {
    var config = window.__APP_CONFIG || {};
    return Array.isArray(config.authGuideSteps) && config.authGuideSteps.length
      ? config.authGuideSteps
      : defaultAuthGuideSteps;
  }

  function getEl(id) {
    return document.getElementById(id);
  }

  async function readJSON(response) {
    var data = await response.json().catch(function () { return null; });
    if (!response.ok) {
      throw new Error((data && data.session && data.session.error) || "请求失败");
    }
    return data || {};
  }

  function renderSteps() {
    var stepsRoot = getEl("steps");
    if (!stepsRoot) return;
    stepsRoot.innerHTML = "";

    authGuideSteps().forEach(function (text, index) {
      var row = document.createElement("div");
      row.className = "auth-step";

      var number = document.createElement("div");
      number.className = "auth-step-number";
      number.textContent = String(index + 1);

      var body = document.createElement("div");
      body.className = "auth-step-body";
      body.textContent = text;

      if (index === 0) {
        var action = document.createElement("div");
        action.className = "auth-step-action";

        var button = document.createElement("button");
        button.id = "openAuth";
        button.className = "auth-button auth-button-primary auth-button-compact";
        button.type = "button";
        button.textContent = "打开授权页面";
        button.addEventListener("click", openAuth);

        action.appendChild(button);
        body.appendChild(action);
      }

      row.appendChild(number);
      row.appendChild(body);
      stepsRoot.appendChild(row);
    });
  }

  async function fetchStatus() {
    var response = await fetch("/api/codex-auth/status", { credentials: "same-origin" });
    return readJSON(response);
  }

  function renderStatus(data) {
    if (!data) return;
    if (data.loggedIn) {
      var openButton = getEl("openAuth");
      if (openButton) {
        openButton.disabled = true;
      }
      return;
    }

    var session = data.session || {};
    if (session.id) {
      currentSessionId = session.id;
    }
  }

  async function openAuth() {
    var button = getEl("openAuth");
    if (!button) return;

    var keepDisabled = false;
    button.disabled = true;
    try {
      var response = await fetch("/api/codex-auth/start?restart=1", {
        method: "POST",
        credentials: "same-origin",
      });
      var data = await readJSON(response);
      renderStatus(data);

      if (data.loggedIn) {
        keepDisabled = true;
        alert("当前设备已登录。");
        return;
      }

      var authUrl = data && data.session && data.session.authUrl;
      if (!authUrl) {
        alert((data.session && data.session.error) || "当前没有可用的授权链接，请重试。");
        return;
      }

      window.open(authUrl, "_blank", "noopener,noreferrer");
    } catch (err) {
      alert("生成授权链接失败。");
    } finally {
      button.disabled = keepDisabled;
      ensurePolling();
    }
  }

  async function completeAuth() {
    var button = getEl("completeAuth");
    var input = getEl("callbackInput");
    if (!button || !input) return;

    var callbackUrl = String(input.value || "").trim();
    if (!callbackUrl) {
      alert("请先粘贴授权完成后的回调链接。");
      return;
    }

    button.disabled = true;
    try {
      var response = await fetch("/api/codex-auth/complete", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ sessionId: currentSessionId, callbackUrl: callbackUrl }),
      });
      var data = await readJSON(response);
      renderStatus(data);

      if (data.loggedIn) {
        alert("验证成功，请返回 Code Web。");
        stopPolling();
        return;
      }

      alert((data.session && data.session.error) || "授权还未完成，请检查回调链接是否完整。");
    } catch (err) {
      alert("提交回调链接失败。");
    } finally {
      button.disabled = false;
    }
  }

  function stopPolling() {
    if (!pollTimer) return;
    clearInterval(pollTimer);
    pollTimer = null;
  }

  function ensurePolling() {
    if (pollTimer) return;
    pollTimer = setInterval(async function () {
      try {
        var data = await fetchStatus();
        renderStatus(data);
        if (data.loggedIn) {
          stopPolling();
        }
      } catch (err) {
      }
    }, 2000);
  }

  async function init() {
    renderSteps();
    var completeButton = getEl("completeAuth");
    if (completeButton) {
      completeButton.addEventListener("click", completeAuth);
    }

    try {
      var data = await fetchStatus();
      renderStatus(data);
      if (!data.loggedIn && data.session && data.session.id) {
        ensurePolling();
      }
    } catch (err) {
    }
  }

  init();
})();
