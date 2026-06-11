package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// errNoRowsForTest 复用 sql.ErrNoRows,模拟 store 按 id 未命中时返回的标准“无行”错误。
var errNoRowsForTest = sql.ErrNoRows

// fakeBlobs 是 LibraryBlobStore 的内存实现,供工单消息与定制技能 service 单测复用。
type fakeBlobs struct{ data map[string][]byte }

func newFakeBlobs() *fakeBlobs { return &fakeBlobs{data: map[string][]byte{}} }
func (f *fakeBlobs) PutLibrarySkill(source, ref, version, ext string, data []byte) (string, error) {
	key := "library/" + source + "/" + ref + "/" + version + "." + ext
	f.data[key] = append([]byte(nil), data...)
	return key, nil
}
func (f *fakeBlobs) DeleteLibrarySkill(rel string) error { delete(f.data, rel); return nil }
func (f *fakeBlobs) OpenLibrarySkill(rel string) (io.ReadCloser, error) {
	b, ok := f.data[rel]
	if !ok {
		return nil, errNoRowsForTest
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}

// memberAP 返回企业成员主体,用于定制技能与工单消息测试中的需求方身份。
func memberAP() auth.Principal {
	return auth.Principal{UserID: "u-mem", OrgID: "org-1", Role: domain.UserRoleOrgMember}
}

// fakeMessageStore 是 SkillTicketMessageStore 的内存实现,记录消息落库、状态重开和 touch 行为。
type fakeMessageStore struct {
	tickets  map[string]sqlc.SkillTicket
	messages map[string]sqlc.SkillTicketMessage
	order    []string
	touched  []string
}

func newFakeMessageStore() *fakeMessageStore {
	return &fakeMessageStore{
		tickets:  map[string]sqlc.SkillTicket{},
		messages: map[string]sqlc.SkillTicketMessage{},
	}
}

func (f *fakeMessageStore) GetSkillTicket(_ context.Context, id string) (sqlc.SkillTicket, error) {
	t, ok := f.tickets[id]
	if !ok {
		return sqlc.SkillTicket{}, sql.ErrNoRows
	}
	return t, nil
}

func (f *fakeMessageStore) UpdateSkillTicketStatus(_ context.Context, a sqlc.UpdateSkillTicketStatusParams) error {
	t := f.tickets[a.ID]
	t.Status = a.Status
	f.tickets[a.ID] = t
	return nil
}

func (f *fakeMessageStore) TouchSkillTicket(_ context.Context, id string) error {
	f.touched = append(f.touched, id)
	return nil
}

func (f *fakeMessageStore) CreateSkillTicketMessage(_ context.Context, a sqlc.CreateSkillTicketMessageParams) error {
	created := time.Date(2026, 6, 11, 10, 0, len(f.order), 0, time.UTC)
	f.messages[a.ID] = sqlc.SkillTicketMessage{
		ID: a.ID, TicketID: a.TicketID, AuthorUserID: a.AuthorUserID,
		Kind: a.Kind, Body: a.Body, CreatedAt: created,
	}
	f.order = append(f.order, a.ID)
	return nil
}

func (f *fakeMessageStore) ListSkillTicketMessages(_ context.Context, ticketID string) ([]sqlc.SkillTicketMessage, error) {
	out := make([]sqlc.SkillTicketMessage, 0, len(f.order))
	for _, id := range f.order {
		row := f.messages[id]
		if row.TicketID == ticketID {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeMessageStore) GetSkillTicketMessage(_ context.Context, id string) (sqlc.SkillTicketMessage, error) {
	row, ok := f.messages[id]
	if !ok {
		return sqlc.SkillTicketMessage{}, sql.ErrNoRows
	}
	return row, nil
}

// addMessageTicket 预置一张工单,覆盖消息权限与自动重开测试的工单上下文。
func addMessageTicket(store *fakeMessageStore, status string) {
	store.tickets["ticket-1"] = sqlc.SkillTicket{
		ID: "ticket-1", OrgID: "org-1", RequesterUserID: "u-mem",
		RequesterRole: domain.UserRoleOrgMember, Status: status,
	}
}

// TestMessageService_SendText 覆盖文本消息写入:text kind 与 {"text":...} JSON body 均应落库。
func TestMessageService_SendText(t *testing.T) {
	store := newFakeMessageStore()
	addMessageTicket(store, SkillTicketStatusPending)
	svc := NewSkillTicketMessageService(store, newFakeBlobs())

	got, err := svc.SendText(context.Background(), memberAP(), "ticket-1", " 你好 ")
	require.NoError(t, err)
	assert.Equal(t, MessageKindText, got.Kind)
	assert.Equal(t, "你好", got.Text)

	var body struct {
		Text string `json:"text"`
	}
	require.NoError(t, json.Unmarshal(store.messages[got.ID].Body, &body))
	assert.Equal(t, "你好", body.Text)
}

// TestMessageService_RequesterMessageReopensClosed 覆盖需求方在已关闭工单补充消息时自动重开为 pending。
func TestMessageService_RequesterMessageReopensClosed(t *testing.T) {
	store := newFakeMessageStore()
	addMessageTicket(store, SkillTicketStatusDelivered)
	svc := NewSkillTicketMessageService(store, newFakeBlobs())

	_, err := svc.SendText(context.Background(), memberAP(), "ticket-1", "补充需求")
	require.NoError(t, err)

	assert.Equal(t, SkillTicketStatusPending, store.tickets["ticket-1"].Status)
	assert.Empty(t, store.touched)
}

// TestMessageService_AdminMessageDoesNotReopen 覆盖平台管理员在已交付工单发消息时只刷新时间,不改变关闭状态。
func TestMessageService_AdminMessageDoesNotReopen(t *testing.T) {
	store := newFakeMessageStore()
	addMessageTicket(store, SkillTicketStatusDelivered)
	svc := NewSkillTicketMessageService(store, newFakeBlobs())

	_, err := svc.SendText(context.Background(), adminP(), "ticket-1", "已收到")
	require.NoError(t, err)

	assert.Equal(t, SkillTicketStatusDelivered, store.tickets["ticket-1"].Status)
	assert.Equal(t, []string{"ticket-1"}, store.touched)
}

// TestMessageService_SendFileKindByContentType 覆盖 content_type 判别:image/* 生成 image,其他文件生成 file。
func TestMessageService_SendFileKindByContentType(t *testing.T) {
	store := newFakeMessageStore()
	addMessageTicket(store, SkillTicketStatusPending)
	svc := NewSkillTicketMessageService(store, newFakeBlobs())

	img, err := svc.SendFile(context.Background(), memberAP(), "ticket-1", "截图.png", "image/png", []byte("png"))
	require.NoError(t, err)
	assert.Equal(t, MessageKindImage, img.Kind)

	pdf, err := svc.SendFile(context.Background(), memberAP(), "ticket-1", "说明.pdf", "application/pdf", []byte("pdf"))
	require.NoError(t, err)
	assert.Equal(t, MessageKindFile, pdf.Kind)
}

// TestMessageService_ListAndPermission 覆盖消息列表解析三类 body,且非提交者/非平台管理员无权查看。
func TestMessageService_ListAndPermission(t *testing.T) {
	store := newFakeMessageStore()
	addMessageTicket(store, SkillTicketStatusPending)
	svc := NewSkillTicketMessageService(store, newFakeBlobs())
	_, err := svc.SendText(context.Background(), memberAP(), "ticket-1", "文字")
	require.NoError(t, err)
	_, err = svc.SendFile(context.Background(), memberAP(), "ticket-1", "图.png", "image/png", []byte("img"))
	require.NoError(t, err)
	_, err = svc.SendFile(context.Background(), memberAP(), "ticket-1", "文档.pdf", "application/pdf", []byte("pdf"))
	require.NoError(t, err)

	got, err := svc.ListMessages(context.Background(), memberAP(), "ticket-1")
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, MessageKindText, got[0].Kind)
	assert.Equal(t, "文字", got[0].Text)
	assert.Equal(t, MessageKindImage, got[1].Kind)
	assert.Equal(t, "图.png", got[1].FileName)
	assert.Equal(t, MessageKindFile, got[2].Kind)
	assert.Equal(t, "文档.pdf", got[2].FileName)

	other := auth.Principal{UserID: "u-other", OrgID: "org-1", Role: domain.UserRoleOrgMember}
	_, err = svc.ListMessages(context.Background(), other, "ticket-1")
	require.ErrorIs(t, err, ErrSkillTicketDenied)
}

// TestMessageService_DownloadFileMessage 覆盖文件消息下载与 text 消息不可下载的异常路径。
func TestMessageService_DownloadFileMessage(t *testing.T) {
	store := newFakeMessageStore()
	addMessageTicket(store, SkillTicketStatusPending)
	blobs := newFakeBlobs()
	svc := NewSkillTicketMessageService(store, blobs)

	fileMsg, err := svc.SendFile(context.Background(), memberAP(), "ticket-1", "说明.pdf", "application/pdf", []byte("pdf-bytes"))
	require.NoError(t, err)
	data, name, contentType, err := svc.DownloadFile(context.Background(), memberAP(), "ticket-1", fileMsg.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("pdf-bytes"), data)
	assert.Equal(t, "说明.pdf", name)
	assert.Equal(t, "application/pdf", contentType)

	textMsg, err := svc.SendText(context.Background(), memberAP(), "ticket-1", "文字")
	require.NoError(t, err)
	_, _, _, err = svc.DownloadFile(context.Background(), memberAP(), "ticket-1", textMsg.ID)
	require.ErrorIs(t, err, ErrSkillTicketInvalid)
}
