<template>
  <span class="url-links">
    <template v-if="list.length">
      <a
        v-for="item in list"
        :key="item.raw"
        :href="item.href"
        target="_blank"
        rel="noopener noreferrer"
        class="url-link mono"
        :title="item.href"
        >{{ item.raw }}</a
      >
    </template>
    <span v-else class="text-muted">—</span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'

/**
 * 访问入口链接组：把采集到的 URL 渲染成可点击外链。
 * 后端可能给出 `10.0.0.1:3000` 这类无协议头的地址，此处统一补 http://。
 */
const props = withDefaults(defineProps<{ urls?: string[] | null }>(), { urls: () => [] })

interface UrlItem {
  raw: string
  href: string
}

const list = computed<UrlItem[]>(() =>
  (props.urls || [])
    .map((u) => (u || '').trim())
    .filter((u) => u !== '' && u !== '-' && u !== '—')
    .map((raw) => ({ raw, href: /^https?:\/\//i.test(raw) ? raw : `http://${raw}` })),
)
</script>

<style scoped>
.url-links {
  display: inline-flex;
  flex-direction: column;
  gap: 2px;
}

.url-link {
  color: #2f6fed;
  text-decoration: none;
  word-break: break-all;
}

.url-link:hover {
  text-decoration: underline;
}
</style>
