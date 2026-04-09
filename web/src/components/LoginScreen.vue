<script setup>
defineProps({
  appName: String,
  version: String,
  password: String,
  error: String
});

const emit = defineEmits(["update:password", "submit"]);
</script>

<template>
  <section class="overlay-screen">
    <form class="auth-card" @submit.prevent="emit('submit')">
      <div class="brand-mark">CW</div>
      <h1>{{ appName }}</h1>
      <p>{{ version }}</p>
      <input class="visually-hidden" type="text" name="username" autocomplete="username" />
      <input
        :value="password"
        id="login-password"
        name="password"
        class="text-input"
        type="password"
        placeholder="请输入密码"
        autocomplete="current-password"
        @input="emit('update:password', $event.target.value)"
      />
      <button class="primary-btn" type="submit">登录</button>
      <div class="auth-error">{{ error }}</div>
    </form>
  </section>
</template>
