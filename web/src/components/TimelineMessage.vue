<script setup>
import { markdownHtml, previewImages } from "../lib/chat-helpers.js";

defineProps({
  item: Object,
  formatTime: Function
});
</script>

<template>
  <article class="timeline-row" :class="`role-${item.value.role}`">
    <div class="timeline-meta">{{ formatTime(item.value.createdAt) }}</div>
    <div v-if="previewImages(item.value).length" class="message-images">
      <img v-for="url in previewImages(item.value)" :key="url" :src="url" alt="upload" />
    </div>
    <div class="message-bubble" :class="`bubble-${item.value.role}`">
      <div v-if="item.value.role === 'assistant'" class="markdown-body" v-html="markdownHtml(item.value.content)" />
      <template v-else>{{ item.value.content }}</template>
    </div>
  </article>
</template>
