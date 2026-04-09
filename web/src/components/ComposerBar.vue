<script setup>
import { ref } from "vue";

const fileInputRef = ref(null);

defineProps({
  state: Object,
  modelOptions: Array,
  canSend: Boolean
});

const emit = defineEmits(["submit", "paste", "image-change", "remove-image", "update:input"]);
</script>

<template>
  <form class="composer-card" @submit.prevent="emit('submit')">
    <div class="composer-topline">
      <div class="composer-status">
        <div class="footer-state">{{ state.footerState }}</div>
        <div class="footer-detail">{{ state.footerDetail }}</div>
      </div>
      <select v-model="state.currentModel" class="model-select" :disabled="state.running">
        <option v-for="model in modelOptions" :key="model" :value="model">{{ model }}</option>
      </select>
    </div>

    <div v-if="state.pendingImages.length" class="attachment-strip">
      <div v-for="(item, index) in state.pendingImages" :key="item.url" class="attachment-chip">
        <img :src="item.url" alt="preview" />
        <button type="button" @click="emit('remove-image', index)">×</button>
      </div>
    </div>

    <div class="composer-main">
      <textarea
        :value="state.input"
        id="composer-input"
        name="prompt"
        class="prompt-input"
        rows="1"
        spellcheck="false"
        placeholder="发送消息，Shift + Enter 换行"
        @input="emit('update:input', $event.target.value)"
        @keydown.enter.exact.prevent="emit('submit')"
        @paste="emit('paste', $event)"
      />
      <input ref="fileInputRef" type="file" accept="image/*" multiple hidden @change="emit('image-change', $event)" />
      <button class="composer-icon secondary" type="button" :disabled="state.running" @click="fileInputRef?.click()">
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <rect x="4.5" y="5.5" width="15" height="13" rx="2.5" />
          <circle cx="9" cy="10" r="1.5" />
          <path d="M7 16l3.2-3.2a1 1 0 0 1 1.4 0L13 14l1.7-1.7a1 1 0 0 1 1.4 0L17.5 14" />
        </svg>
      </button>
      <button class="composer-icon" type="submit" :disabled="!canSend">
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M12 5v11" />
          <path d="M8.5 9.5 12 5l3.5 4.5" />
        </svg>
      </button>
    </div>
  </form>
</template>
