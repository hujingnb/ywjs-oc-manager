package storage

// 本文件给 S3ObjectStore 增加标准 S3 multipart upload 能力（分片初始化 / 上传分片 /
// 合并 / 中止），以及合并后对象的流式读取与删除。仅 S3ObjectStore（真实对象存储）实现，
// 不挂到 ObjectStore 接口上——本地 FS 兜底无法提供服务端分片合并，分片上传仅在 S3 启用时可用。
//
// 用途：知识库大文件上传走分片，前端顺序逐片传给 manager，manager 用 multipart 暂存到对象
// 存储并服务端合并，完成时再流式推给 RAGFlow，规避「单请求一次传完」撞代理超时的问题。

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// MultipartPart 是一个已上传分片的标识：S3 合并时按 PartNumber 排序、用 ETag 校验。
type MultipartPart struct {
	PartNumber int32  // 分片序号，从 1 开始连续递增
	ETag       string // UploadPart 返回的分片 ETag
}

// CreateMultipartUpload 在对象存储发起一次分片上传，返回 S3 侧的 UploadId（后续分片与合并都要带上）。
func (s *S3ObjectStore) CreateMultipartUpload(ctx context.Context, key string) (string, error) {
	out, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", fmt.Errorf("storage: 发起分片上传 %s 失败: %w", key, err)
	}
	return aws.ToString(out.UploadId), nil
}

// UploadPart 上传单个分片；size 为分片字节数提示（用于预分配缓冲）。返回该分片 ETag。
// partNumber 必须在 1..10000；非末片需 ≥5MB（S3 协议约束），由调用方按固定分片大小保证。
//
// 实现细节：aws-sdk-go-v2 对 UploadPart 默认要计算 SigV4 payload 哈希，需要可 seek 的 body 以便
// 哈希后回绕；而上传 body 是不可 seek 的网络流（且经限速包装），直接传会报「failed to compute
// payload hash」。故先把分片读进内存再用 bytes.Reader 交给 SDK——分片有上限（调用方限定，数十 MB），
// 内存可控；真正的大文件流式只在 complete 阶段从对象存储流式推 RAGFlow 时发生，不在此处。
func (s *S3ObjectStore) UploadPart(ctx context.Context, key, uploadID string, partNumber int32, r io.Reader, size int64) (string, error) {
	buf := bytes.NewBuffer(make([]byte, 0, maxPartBufferHint(size)))
	if _, err := io.Copy(buf, r); err != nil {
		return "", fmt.Errorf("storage: 读取分片 %s#%d 失败: %w", key, partNumber, err)
	}
	data := buf.Bytes()
	out, err := s.client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		UploadId:      aws.String(uploadID),
		PartNumber:    aws.Int32(partNumber),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
	})
	if err != nil {
		return "", fmt.Errorf("storage: 上传分片 %s#%d 失败: %w", key, partNumber, err)
	}
	return aws.ToString(out.ETag), nil
}

// maxPartBufferHint 把 size 提示限制到合理范围作为缓冲预分配容量；非正数时回退 0，由 Buffer 自增长。
func maxPartBufferHint(size int64) int {
	const cap = 16 * 1024 * 1024 // 预分配上限，超大提示不一次性占用过多内存
	if size <= 0 {
		return 0
	}
	if size > cap {
		return cap
	}
	return int(size)
}

// CompleteMultipartUpload 按 PartNumber 升序提交全部分片，触发对象存储服务端合并为完整对象。
func (s *S3ObjectStore) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []MultipartPart) error {
	if len(parts) == 0 {
		return fmt.Errorf("storage: 合并分片 %s 缺少分片列表", key)
	}
	// 复制后排序，避免改动调用方切片；S3 要求 Parts 按 PartNumber 升序。
	sorted := append([]MultipartPart(nil), parts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].PartNumber < sorted[j].PartNumber })
	completed := make([]types.CompletedPart, 0, len(sorted))
	for _, p := range sorted {
		completed = append(completed, types.CompletedPart{
			ETag:       aws.String(p.ETag),
			PartNumber: aws.Int32(p.PartNumber),
		})
	}
	if _, err := s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(s.bucket),
		Key:             aws.String(key),
		UploadId:        aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: completed},
	}); err != nil {
		return fmt.Errorf("storage: 合并分片 %s 失败: %w", key, err)
	}
	return nil
}

// AbortMultipartUpload 中止分片上传并让对象存储回收已上传分片（会话失败 / 取消时调用）。
func (s *S3ObjectStore) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	if _, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	}); err != nil {
		return fmt.Errorf("storage: 中止分片上传 %s 失败: %w", key, err)
	}
	return nil
}

// OpenObject 打开对象的只读流与字节数，供合并后流式推送给 RAGFlow（不把整文件读进内存）。
// 调用方负责 Close 返回的 ReadCloser。
func (s *S3ObjectStore) OpenObject(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("storage: 读取对象 %s 失败: %w", key, err)
	}
	return out.Body, aws.ToInt64(out.ContentLength), nil
}

// DeleteObject 删除单个对象（合并并推送 RAGFlow 成功后清理暂存对象用）。
func (s *S3ObjectStore) DeleteObject(ctx context.Context, key string) error {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}); err != nil {
		return fmt.Errorf("storage: 删除对象 %s 失败: %w", key, err)
	}
	return nil
}
