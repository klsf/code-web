<script setup>
import TimelineMessage from "./TimelineMessage.vue";
import EventGroup from "./EventGroup.vue";
import ComposerBar from "./ComposerBar.vue";

defineProps({
  state: Object,
  sessionLabel: String,
  selectedProvider: String,
  connectionInfo: Object,
  showConnectionBanner: Boolean,
  mergedTimeline: Array,
  modelOptions: Array,
  canSend: Boolean,
  formatTime: Function,
  actionLabel: Function,
  groupPreviewIcons: Function,
  isGroupExpanded: Function,
  selectedEventId: Function,
  toggleGroupExpanded: Function,
  toggleEventDetail: Function,
  eventRowLabel: Function
});

const emit = defineEmits(["open-menu", "submit", "paste", "image-change", "remove-image", "update:input"]);
</script>

<template>
  <div class="workspace-shell">
    <main class="chat-shell">
      <header class="chat-header">
        <div class="chat-header-main">
          <button class="ghost-icon" type="button" aria-label="打开菜单" @click="emit('open-menu')">
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path d="M5 7h14" />
              <path d="M5 12h14" />
              <path d="M5 17h14" />
            </svg>
          </button>
          <div class="chat-header-copy">
            <div class="chat-title-row">
              <div class="chat-title">{{ state.appName }}</div>
            </div>
            <div class="chat-subtitle chat-subtitle-desktop">
              <span>{{ sessionLabel }}</span>
              <span class="dot">•</span>
              <span>{{ selectedProvider }}</span>
            </div>
          </div>
        </div>
        <div class="chat-header-side">
          <div class="header-inline-chip">
            <span>{{ sessionLabel }}</span>
            <span class="dot">/</span>
            <span>{{ selectedProvider }}</span>
          </div>
          <div class="header-pill">{{ state.running ? "Working" : "Ready" }}</div>
        </div>
      </header>

      <section v-if="showConnectionBanner" class="connection-banner">
        <div class="connection-badge">{{ connectionInfo.badge }}</div>
        <div>
          <div class="connection-title">{{ connectionInfo.title }}</div>
          <div class="connection-detail">{{ connectionInfo.detail }}</div>
        </div>
      </section>

      <section class="timeline-panel" aria-live="polite">
        <div class="timeline-inner">
          <template v-if="mergedTimeline.length">
            <template v-for="item in mergedTimeline" :key="item.id || item.value?.id || item.value?.createdAt">
              <TimelineMessage v-if="item.kind === 'message'" :item="item" :format-time="formatTime" />
              <EventGroup
                v-else-if="item.kind === 'event-group'"
                :item="item"
                :format-time="formatTime"
                :action-label="actionLabel"
                :group-preview-icons="groupPreviewIcons"
                :is-group-expanded="isGroupExpanded"
                :selected-event-id="selectedEventId"
                :toggle-group-expanded="toggleGroupExpanded"
                :toggle-event-detail="toggleEventDetail"
                :event-row-label="eventRowLabel"
              />
              <article
                v-else-if="item.value?.content?.trim?.()"
                class="timeline-row role-assistant draft-row"
              >
                <div class="timeline-meta">生成中</div>
                <div class="message-bubble bubble-assistant draft-bubble">{{ item.value.content }}</div>
              </article>
            </template>
          </template>

          <div v-else class="empty-state">
            <div class="empty-title">准备开始</div>
            <div class="empty-copy">你可以给我分配一个任务，我会尽力完成它。</div>
          </div>
        </div>
      </section>

      <ComposerBar
        :state="state"
        :model-options="modelOptions"
        :can-send="canSend"
        @submit="emit('submit')"
        @paste="emit('paste', $event)"
        @image-change="emit('image-change', $event)"
        @remove-image="emit('remove-image', $event)"
        @update:input="emit('update:input', $event)"
      />
    </main>
  </div>
</template>
