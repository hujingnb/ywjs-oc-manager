// 后端返回的 DTO 类型。
// 字段命名与 OpenAPI 保持一致，便于 service 层按 OpenAPI 演进时自动同步。

export interface AuthUser {
  id: string
  org_id?: string
  username: string
  display_name: string
  role: 'platform_admin' | 'org_admin' | 'org_member'
  status: 'active' | 'disabled'
}

export interface TokenPair {
  access_token: string
  refresh_token: string
}

export interface LoginResult {
  user: AuthUser
  tokens: TokenPair
}

export interface Organization {
  id: string
  name: string
  status: 'active' | 'disabled' | 'deleted'
  contact_name?: string
  contact_phone?: string
  remark?: string
  newapi_user_id?: string
  credit_warning_threshold?: number
}

export interface Member {
  id: string
  org_id?: string
  username: string
  display_name: string
  role: 'platform_admin' | 'org_admin' | 'org_member'
  status: 'active' | 'disabled'
}

export interface RuntimeNode {
  id: string
  name: string
  status: 'pending' | 'active' | 'unreachable' | 'disabled'
  agent_docker_endpoint?: string
  agent_file_endpoint?: string
  agent_version?: string
  heartbeat_interval_seconds: number
  node_data_root?: string
  bootstrap_token?: string
  bootstrap_token_expires_at?: string
  has_agent_token: boolean
  // 节点最大未删除应用数；undefined / null 表示不限。
  // 由 platform_admin 通过 PATCH /runtime-nodes/:id 设置；OnboardingService 自动选节点时按剩余容量过滤。
  max_apps?: number | null
}

export interface AuditLog {
  id: string
  actor_id?: string
  actor_role: string
  org_id?: string
  target_type: string
  target_id: string
  action: string
  result: string
  error_message?: string
  ip_address?: string
  metadata?: Record<string, unknown>
  created_at: string
}
