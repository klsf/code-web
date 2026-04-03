var timeline = document.getElementById("timeline");
var loginScreen = document.getElementById("loginScreen");
var errorToast = document.getElementById("errorToast");
var versionNodes = document.querySelectorAll("[data-version]");
var appTitleNodes = document.querySelectorAll("[data-app-title]");
var loginForm = document.getElementById("loginForm");
var passwordInput = document.getElementById("passwordInput");
var loginError = document.getElementById("loginError");
var sessionChooser = document.getElementById("sessionChooser");
var providerPicker = document.getElementById("providerPicker");
var newSessionChoice = document.getElementById("newSessionChoice");
var resumeSessionChoice = document.getElementById("resumeSessionChoice");
var resumeList = document.getElementById("resumeList");
var resumeEmpty = document.getElementById("resumeEmpty");
var workdirInput = document.getElementById("workdirInput");
var form = document.getElementById("composer");
var input = document.getElementById("promptInput");
var sessionBadge = document.getElementById("sessionBadge");
var desktopSessionBadge = document.getElementById("desktopSessionBadge");
var modelBadge = document.getElementById("modelBadge");
var cwdBadge = document.getElementById("cwdBadge");
var transportBadge = document.getElementById("transportBadge");
var desktopTransportBadge = document.getElementById("desktopTransportBadge");
var connectionBanner = document.getElementById("connectionBanner");
var connectionBadge = document.getElementById("connectionBadge");
var connectionTitle = document.getElementById("connectionTitle");
var connectionDetail = document.getElementById("connectionDetail");
var attachmentTray = document.getElementById("attachmentTray");
var commandPalette = document.getElementById("commandPalette");
var imageInput = document.getElementById("imageInput");
var imageBtn = document.getElementById("imageBtn");
var sendBtn = document.getElementById("sendBtn");
var footerState = document.getElementById("footerState");
var footerDetail = document.getElementById("footerDetail");
var statusSession = document.getElementById("statusSession");
var statusProvider = document.getElementById("statusProvider");
var statusModel = document.getElementById("statusModel");
var statusCwd = document.getElementById("statusCwd");
var statusTransport = document.getElementById("statusTransport");
var statusTask = document.getElementById("statusTask");
var statusApprovals = document.getElementById("statusApprovals");
var statusFast = document.getElementById("statusFast");
var statusServiceTier = document.getElementById("statusServiceTier");
var statusPlan = document.getElementById("statusPlan");
var statusPrimary = document.getElementById("statusPrimary");
var statusSecondary = document.getElementById("statusSecondary");
var statusCredits = document.getElementById("statusCredits");
var template = document.getElementById("messageTemplate");
var attachmentTemplate = document.getElementById("attachmentTemplate");

var ws = null;
var wsIntentionalClose = false;
var reconnectTimer = null;
var currentSessionId =
  localStorage.getItem("codex_session_id") ||
  localStorage.getItem("sessionId") ||
  localStorage.getItem("sessionid") ||
  sessionStorage.getItem("codex_session_id") ||
  sessionStorage.getItem("sessionId") ||
  sessionStorage.getItem("sessionid") ||
  "";
var isRunning = false;
var pendingImages = [];
var activeDraftId = "";
var streamStates = new Map();
var commandItems = [];
var commandIndex = 0;
var paletteMode = "commands";
var errorToastTimer = null;
var isAuthenticated = false;
var commands = [];
var currentProvider = (window.__APP_CONFIG && window.__APP_CONFIG.provider) || "codex";
