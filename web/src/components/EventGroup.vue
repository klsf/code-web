<script setup>
import { eventDetailSections, eventTitleText, iconMap } from "../lib/chat-helpers.js";

defineProps({
  item: Object,
  formatTime: Function,
  actionLabel: Function,
  groupPreviewIcons: Function,
  isGroupExpanded: Function,
  selectedEventId: Function,
  toggleGroupExpanded: Function,
  toggleEventDetail: Function,
  eventRowLabel: Function
});
</script>

<template>
  <article class="timeline-row role-system event-group-row">
    <div class="timeline-meta">{{ formatTime(item.createdAt) }}</div>
    <div class="event-group-card">
      <button class="event-group-summary" type="button" @click="toggleGroupExpanded(item)">
        <span class="event-group-summary-left">
          <span class="event-group-preview">
            <span
              v-for="event in groupPreviewIcons(item)"
              :key="`${item.id}-${event.id}-preview`"
              class="event-preview-icon"
              :class="event.variant"
            >
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <path v-for="path in iconMap[event.variant] || iconMap.status" :key="path" :d="path" />
              </svg>
            </span>
          </span>
          <span class="event-group-count">{{ actionLabel(item) }}</span>
        </span>
        <span class="event-group-chevron">{{ isGroupExpanded(item) ? "⌃" : "⌄" }}</span>
      </button>
      <div v-if="isGroupExpanded(item)" class="event-detail-list">
        <div
          v-for="event in item.events"
          :key="`${event.id}-detail`"
          class="event-detail-item"
          :class="{ active: selectedEventId(item) === event.id }"
        >
          <button class="event-detail-toggle" type="button" @click="toggleEventDetail(item, event.id)">
            <span class="event-detail-leading">
              <span class="event-detail-icon" :class="event.variant">
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <path v-for="path in iconMap[event.variant] || iconMap.status" :key="path" :d="path" />
                </svg>
              </span>
              <span class="event-detail-copy">{{ eventRowLabel(event) }}</span>
            </span>
            <span class="event-detail-chevron">{{ selectedEventId(item) === event.id ? "⌃" : "⌄" }}</span>
          </button>
          <div v-if="selectedEventId(item) === event.id" class="event-detail-panel">
            <div class="event-detail-title">{{ eventTitleText(event) }}</div>
            <div class="event-detail-meta">{{ event.meta }}</div>
            <div class="event-detail-sections">
              <section
                v-for="(section, index) in eventDetailSections(event)"
                :key="`${event.id}-section-${index}`"
                class="event-detail-section"
                :class="`tone-${section.tone}`"
              >
                <div class="event-detail-section-label">{{ section.label }}</div>
                <pre class="event-detail-body">{{ section.value }}</pre>
              </section>
            </div>
          </div>
        </div>
      </div>
    </div>
  </article>
</template>
