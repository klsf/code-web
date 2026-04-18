<script setup>
defineProps({
  appName: String,
  version: String,
  providers: Array,
  currentProvider: String,
  chooserWorkdir: String,
  hasSessions: Boolean,
  providerIconPaths: Function
});

const emit = defineEmits(["update:provider", "update:workdir", "create", "resume"]);
</script>

<template>
  <section class="overlay-screen">
    <div class="auth-card chooser-card">
      <h1>{{ appName }}</h1>
      <p>{{ version }}</p>
      <div class="provider-grid">
        <button
          v-for="provider in providers"
          :key="provider.id"
          class="provider-card"
          :class="{ selected: currentProvider === provider.id }"
          type="button"
          @click="emit('update:provider', provider.id)"
        >
          <svg viewBox="0 0 48 48" aria-hidden="true">
            <path v-for="path in providerIconPaths(provider.id)" :key="path" :d="path" />
          </svg>
          <span>{{ provider.name }}</span>
        </button>
      </div>
      <input
        :value="chooserWorkdir"
        id="chooser-workdir"
        name="workdir"
        class="text-input"
        type="text"
        placeholder="请输入工作目录，例如 H:\go\code-web-new"
        @input="emit('update:workdir', $event.target.value)"
      />
      <div class="chooser-actions">
        <button class="primary-btn" type="button" @click="emit('create')">新建会话</button>
        <button class="secondary-btn" type="button" :disabled="!hasSessions" @click="emit('resume')">恢复会话</button>
      </div>
    </div>
  </section>
</template>
