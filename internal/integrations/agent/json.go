package agent

import "encoding/json"

// marshal 将任意 map 序列化为字节切片。
// 抽出独立函数主要用于 endpoints 测试中替换为可观测实现。
func marshal(value any) ([]byte, error) { return json.Marshal(value) }
