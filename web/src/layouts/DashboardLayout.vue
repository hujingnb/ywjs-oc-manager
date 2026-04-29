<template>
  <div class="dashboard-shell">
    <aside class="sidebar" aria-label="主导航">
      <div class="brand-block">
        <div class="brand-mark">OC</div>
        <div>
          <strong>OpenClaw</strong>
          <span>Manager</span>
        </div>
      </div>

      <nav class="nav-list">
        <a class="nav-item active" href="/">
          <LayoutDashboard :size="18" />
          <span>总览</span>
        </a>
        <a class="nav-item" href="/orgs">
          <Building2 :size="18" />
          <span>组织</span>
        </a>
        <a class="nav-item" href="/apps">
          <Bot :size="18" />
          <span>应用</span>
        </a>
        <a class="nav-item" href="/runtime-nodes">
          <Server :size="18" />
          <span>运行节点</span>
        </a>
      </nav>
    </aside>

    <div class="workspace">
      <header class="topbar">
        <div>
          <p class="eyebrow">本地调试环境</p>
          <h1>控制台</h1>
        </div>
        <div class="topbar-actions">
          <span class="status-pill ok">API 正常</span>
          <span class="status-pill warn">Ollama 待配置模型</span>
          <button class="icon-button" type="button" aria-label="刷新">
            <RefreshCw :size="18" />
          </button>
        </div>
      </header>

      <RouterView />
    </div>
  </div>
</template>

<script setup lang="ts">
import { Bot, Building2, LayoutDashboard, RefreshCw, Server } from 'lucide-vue-next'
</script>
