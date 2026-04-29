package service

import "errors"

var (
	ErrForbidden = errors.New("无权执行该操作")
	ErrNotFound  = errors.New("资源不存在")
)
