<script setup lang="ts">
// 根组件只负责路由出口；布局由各路由的 layout 决定。
</script>

<template>
  <router-view v-slot="{ Component }">
    <transition name="ly-fade" mode="out-in">
      <component :is="Component" />
    </transition>
  </router-view>
</template>

<style>
.ly-fade-enter-active,
.ly-fade-leave-active {
  transition: opacity 0.16s ease;
}
.ly-fade-enter-from,
.ly-fade-leave-to {
  opacity: 0;
}
</style>
