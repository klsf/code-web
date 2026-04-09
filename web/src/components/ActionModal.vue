<script setup>
const emit = defineEmits(["close", "sessions", "new", "logout"]);
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal-card action-card">
      <div class="modal-head">
        <strong>快捷操作</strong>
        <button class="ghost-icon small" type="button" @click="emit('close')">×</button>
      </div>
      <button class="action-tile" type="button" @click="emit('sessions')">
        <span>会话列表</span>
        <small>查看、恢复或删除历史会话</small>
      </button>
      <button class="action-tile primary" type="button" @click="emit('new')">
        <span>新建会话</span>
        <small>返回新建页面并开始新的聊天</small>
      </button>
      <button class="action-tile danger" type="button" @click="emit('logout')">
        <span>退出登录</span>
        <small>清除当前登录状态并返回密码登录页</small>
      </button>
    </div>
  </div>
</template>
