<template>
  <!-- AuthLayout 只承载登录相关页面，保持登录流程和后台导航布局隔离。 -->
  <main class="auth-shell">
    <section class="auth-panel" aria-label="登录区域">
      <RouterView />
    </section>
  </main>
</template>
