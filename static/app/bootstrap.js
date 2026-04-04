form.addEventListener("submit", function (evt) {
  evt.preventDefault();
  sendPrompt();
});

loginForm.addEventListener("submit", async function (evt) {
  evt.preventDefault();
  loginError.textContent = "";
  try {
    await submitLogin(passwordInput.value);
    await openSessionChooser();
  } catch (err) {
    loginError.textContent = err && err.message ? err.message : "登录失败";
    showError(loginError.textContent);
  }
});

newSessionChoice.addEventListener("click", async function () {
  try {
    await createSession(workdirInput.value, false, currentProvider);
    enterApp();
  } catch (err) {
    showError(err && err.message ? err.message : "新建会话失败");
  }
});

resumeSessionChoice.addEventListener("click", function () {
  openSessionModal();
});

if (menuBtn) {
  menuBtn.addEventListener("click", function () {
    showActionModal();
  });
}

if (desktopMenuBtn) {
  desktopMenuBtn.addEventListener("click", function () {
    showActionModal();
  });
}

if (actionOpenSessionsBtn) {
  actionOpenSessionsBtn.addEventListener("click", function () {
    hideActionModal();
    openSessionModal();
  });
}

if (actionNewSessionBtn) {
  actionNewSessionBtn.addEventListener("click", function () {
    hideActionModal();
    quickCreateSession();
  });
}

if (actionLogoutBtn) {
  actionLogoutBtn.addEventListener("click", function () {
    logoutAndReset();
  });
}

if (sessionModalCloseBtn) {
  sessionModalCloseBtn.addEventListener("click", function () {
    hideSessionModal();
  });
}

if (sessionModal) {
  sessionModal.addEventListener("click", function (evt) {
    if (evt.target === sessionModal) {
      hideSessionModal();
    }
  });
}

if (actionModalCloseBtn) {
  actionModalCloseBtn.addEventListener("click", function () {
    hideActionModal();
  });
}

if (actionModal) {
  actionModal.addEventListener("click", function (evt) {
    if (evt.target === actionModal) {
      hideActionModal();
    }
  });
}

input.addEventListener("input", function () {
  autoResize();
  updateSendState();
});

input.addEventListener("paste", function (evt) {
  var clipboard = evt.clipboardData;
  if (!clipboard || !clipboard.items || !clipboard.items.length) {
    return;
  }
  var files = [];
  Array.from(clipboard.items).forEach(function (item) {
    if (!item || typeof item.kind !== "string" || item.kind !== "file") {
      return;
    }
    var file = item.getAsFile();
    if (file) {
      files.push(file);
    }
  });
  if (!files.length) {
    return;
  }
  if (addPendingImageFiles(files)) {
    evt.preventDefault();
  }
});

input.addEventListener("keydown", function (evt) {
  if (evt.key === "Enter" && !evt.shiftKey) {
    evt.preventDefault();
    sendPrompt();
  }
});

if (imageBtn && imageInput) {
  imageBtn.addEventListener("click", function () {
    imageInput.click();
  });

  imageInput.addEventListener("change", function () {
    var files = Array.from(imageInput.files || []);
    imageInput.value = "";
    addPendingImageFiles(files);
  });
}

if (modelSelect) {
  modelSelect.addEventListener("change", function () {
    setNodeText(modelBadge, modelSelect.value || "unknown");
    setNodeText(statusModel, modelSelect.value || "unknown");
  });
}

boot();
