// Package service —— hermes_kanban_types.go 原定义 Hermes Kanban CLI --json
// 输出对应的强类型。这些线缆契约 DTO（KanbanBoard / KanbanTask /
// KanbanTaskDetail / KanbanCapabilities 等）已迁至 internal/integrations/ocops
// 包（契约属主），service 引用 ocops.X。service 专属的业务输入 / 过滤类型
// （KanbanTaskFilter / CreateKanbanTaskInput）定义在 hermes_kanban.go 中。
package service
