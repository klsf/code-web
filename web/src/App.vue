<script setup>
import LoginScreen from "./components/LoginScreen.vue";
import SessionChooser from "./components/SessionChooser.vue";
import ChatScreen from "./components/ChatScreen.vue";
import SessionModal from "./components/SessionModal.vue";
import ActionModal from "./components/ActionModal.vue";
import { useChatApp } from "./composables/useChatApp.js";

const {
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
  resumeBadge,
  providerIconPaths
} = useChatApp();
</script>

<template>
  <div class="app-root">
    <transition name="fade">
      <div v-if="state.errorToast" class="error-toast">{{ state.errorToast }}</div>
    </transition>

    <LoginScreen
      v-if="state.screen === 'login'"
      :app-name="state.appName"
      :version="state.version"
      :password="state.loginPassword"
      :error="state.loginError"
      @update:password="state.loginPassword = $event"
      @submit="handleLogin"
    />

    <SessionChooser
      v-else-if="state.screen === 'chooser'"
      :app-name="state.appName"
      :version="state.version"
      :providers="state.providers"
      :current-provider="state.currentProvider"
      :chooser-workdir="state.chooserWorkdir"
      :has-sessions="chooserSessions.length > 0"
      :provider-icon-paths="providerIconPaths"
      @update:provider="state.currentProvider = $event"
      @update:workdir="state.chooserWorkdir = $event"
      @create="handleCreateSession"
      @resume="openSessionModal"
    />

    <ChatScreen
      v-else
      :state="state"
      :session-label="sessionLabel"
      :selected-provider="selectedProvider"
      :connection-info="connectionInfo"
      :show-connection-banner="showConnectionBanner"
      :merged-timeline="mergedTimeline"
      :model-options="modelOptions"
      :can-send="canSend"
      :format-time="formatTime"
      :action-label="actionLabel"
      :group-preview-icons="groupPreviewIcons"
      :is-group-expanded="isGroupExpanded"
      :selected-event-id="selectedEventId"
      :toggle-group-expanded="toggleGroupExpanded"
      :toggle-event-detail="toggleEventDetail"
      :event-row-label="eventRowLabel"
      @open-menu="state.showActionModal = true"
      @submit="sendPrompt"
      @paste="handlePaste"
      @image-change="handleImageSelection"
      @remove-image="removePendingImage"
      @update:input="state.input = $event"
    />

    <SessionModal
      v-if="state.showSessionModal"
      :sessions="chooserSessions"
      :resume-badge="resumeBadge"
      @close="state.showSessionModal = false"
      @open="handleOpenSession"
      @delete="handleDeleteSession"
    />

    <ActionModal
      v-if="state.showActionModal"
      @close="state.showActionModal = false"
      @sessions="state.showActionModal = false; openSessionModal()"
      @new="state.showActionModal = false; openChooser()"
      @logout="handleLogout"
    />
  </div>
</template>
