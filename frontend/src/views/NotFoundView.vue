<template>
  <div class="notfound">
    <el-result icon="warning" title="404" sub-title="页面不存在或已被移除">
      <template #extra>
        <el-button type="primary" @click="router.replace('/')">返回总览</el-button>
      </template>
    </el-result>
  </div>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'

const router = useRouter()
</script>

<style scoped>
.notfound {
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
}
</style>
