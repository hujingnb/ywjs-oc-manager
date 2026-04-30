package redis

import "strconv"

// formatFloatRaw 将浮点数转为 ZRANGE 接受的字符串。
// 抽离出来主要是因为 redis 的 zrange 不接受指数表示法。
func formatFloatRaw(value float64, prec int) string {
	return strconv.FormatFloat(value, 'f', prec, 64)
}
