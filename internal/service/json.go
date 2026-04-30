package service

import "encoding/json"

// jsonMarshal 是 service 包内统一的 JSON 序列化入口。
// 拆出函数主要为了在测试中替换为可观测实现。
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
