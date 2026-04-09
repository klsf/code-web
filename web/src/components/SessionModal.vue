<script setup>
import { compact, formatTime, shortSession } from "../lib/chat-helpers.js";

defineProps({
  sessions: Array,
  resumeBadge: Function
});

const emit = defineEmits(["close", "open", "delete"]);
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal-card">
      <div class="modal-head">
        <strong>会话列表</strong>
        <button class="ghost-icon small" type="button" @click="emit('close')">×</button>
      </div>
      <div class="session-list">
        <div v-for="item in sessions" :key="item.restoreRef?.providerSessionId || item.id" class="session-item">
          <button class="session-open" type="button" @click="emit('open', item)">
            <div class="session-title">{{ item.title || shortSession(item.id) }}</div>
            <div class="session-badges">
              <span class="session-badge">{{ item.restoreRef ? "历史会话" : "当前会话" }}</span>
              <span class="session-badge" :class="{ active: item.running }">{{ resumeBadge(item) }}</span>
            </div>
            <div class="session-desc">{{ [String(item.provider || '').toUpperCase(), compact(item.workdir || item.lastMessage || item.lastEvent || '')].filter(Boolean).join(" · ") }}</div>
            <div class="session-summary">{{ item.updatedAt ? `更新 ${formatTime(item.updatedAt)}` : "" }}</div>
          </button>
          <button class="session-delete" type="button" @click="emit('delete', item)">删除</button>
        </div>
      </div>
    </div>
  </div>
</template>
